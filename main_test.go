package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-imap/server"
	"github.com/firefart/dmarcsyslogforwarder/internal/config"
	"github.com/firefart/dmarcsyslogforwarder/internal/dns"
	"github.com/firefart/dmarcsyslogforwarder/internal/imap"
)

const (
	testUser     = "username"
	testPass     = "password"
	testFolder   = "INBOX"
	rfcName      = "google.com!example.com!1636416000!1636502399"
	recordsCount = 2 // number of records in testdata/report.xml
	// subject of the message that is created by the memory backend
	defaultSubject = "A little message, just for you"
)

type fakeSyslog struct {
	mu      sync.Mutex
	entries [][]byte
	err     error
}

func (f *fakeSyslog) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.entries = append(f.entries, slices.Clone(p))
	return len(p), nil
}

func (f *fakeSyslog) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
}

func startIMAPServer(t *testing.T) string {
	t.Helper()
	s := server.New(memory.New())
	s.AllowInsecureAuth = true
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(l) // nolint: errcheck
	t.Cleanup(func() { _ = s.Close() })
	return l.Addr().String()
}

func testClient(t *testing.T, addr string) *client.Client {
	t.Helper()
	c, err := client.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Logout() })
	if err := c.Login(testUser, testPass); err != nil {
		t.Fatal(err)
	}
	return c
}

func appendMessages(t *testing.T, addr string, messages ...string) {
	t.Helper()
	c := testClient(t, addr)
	for _, m := range messages {
		if err := c.Append(testFolder, nil, time.Now(), bytes.NewBufferString(m)); err != nil {
			t.Fatal(err)
		}
	}
}

// remainingSubjects returns the subjects of all messages that are not deleted
func remainingSubjects(t *testing.T, addr string) []string {
	t.Helper()
	c := testClient(t, addr)
	if _, err := c.Select(testFolder, true); err != nil {
		t.Fatal(err)
	}
	criteria := goimap.NewSearchCriteria()
	criteria.WithoutFlags = []string{goimap.DeletedFlag}
	ids, err := c.Search(criteria)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 {
		return nil
	}
	seqset := new(goimap.SeqSet)
	seqset.AddNum(ids...)
	messages := make(chan *goimap.Message, len(ids))
	if err := c.Fetch(seqset, []goimap.FetchItem{goimap.FetchEnvelope}, messages); err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for m := range messages {
		subjects = append(subjects, m.Envelope.Subject)
	}
	slices.Sort(subjects)
	return subjects
}

func newTestApp(t *testing.T, addr string, sysLog *fakeSyslog, batchSize int) *app {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	settings := config.Configuration{
		Format:    "json",
		BatchSize: batchSize,
		ImapConfig: config.IMAPConfig{
			Host:           addr,
			User:           testUser,
			Pass:           testPass,
			Folder:         testFolder,
			AllowPlaintext: true,
			Timeout:        config.Duration{Duration: 5 * time.Second},
		},
	}
	// use a non existing DNS server so tests do not depend on the network
	resolver := dns.NewCachedDNSResolver(t.Context(), "127.0.0.1:1", 100*time.Millisecond, 200*time.Millisecond, time.Hour, logger)
	return &app{
		sysLog: sysLog,
		dns:    resolver,
		config: settings,
		log:    logger,
	}
}

func loadReport(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "report.xml"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func gzipReport(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(loadReport(t)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipReport(t *testing.T) []byte {
	t.Helper()
	return zipFile(t, rfcName+".xml", loadReport(t))
}

func zipFile(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func b64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func header(subject string) string {
	return "From: dmarc@reporter.example\r\n" +
		"To: dmarc@example.com\r\n" +
		"Subject: " + subject + "\r\n" +
		"Date: Wed, 11 May 2016 14:31:59 +0000\r\n" +
		"MIME-Version: 1.0\r\n"
}

func attachmentMail(subject string, parts ...string) string {
	var sb strings.Builder
	sb.WriteString(header(subject))
	sb.WriteString("Content-Type: multipart/mixed; boundary=\"BOUNDARY\"\r\n\r\n")
	sb.WriteString("--BOUNDARY\r\nContent-Type: text/plain\r\n\r\nThis is a dmarc report\r\n")
	for _, p := range parts {
		sb.WriteString("--BOUNDARY\r\n")
		sb.WriteString(p)
		sb.WriteString("\r\n")
	}
	sb.WriteString("--BOUNDARY--\r\n")
	return sb.String()
}

func attachmentPart(contentType, filename string, content []byte) string {
	disposition := "attachment"
	if filename != "" {
		disposition += fmt.Sprintf("; filename=%q", filename)
	}
	return "Content-Type: " + contentType + "\r\n" +
		"Content-Disposition: " + disposition + "\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64(content) + "\r\n"
}

func inlineMail(subject, filename string, content []byte) string {
	return header(subject) +
		"Content-Type: application/zip\r\n" +
		"Content-Disposition: inline; filename=\"" + filename + "\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64(content) + "\r\n"
}

func textMail(subject string) string {
	return header(subject) + "Content-Type: text/plain\r\n\r\nno report in here\r\n"
}

func reportMail(t *testing.T, subject string) string {
	t.Helper()
	return attachmentMail(subject, attachmentPart("application/gzip", rfcName+".xml.gz", gzipReport(t)))
}

func TestFetchIMAPProcessesAndDeletes(t *testing.T) {
	t.Parallel()

	addr := startIMAPServer(t)
	appendMessages(t, addr,
		reportMail(t, "gz attachment"),
		inlineMail("inline zip", rfcName+".zip", zipReport(t)),
		attachmentMail("signature before report",
			attachmentPart("application/pkcs7-signature", "smime.p7s", []byte("signature")),
			attachmentPart("application/gzip", rfcName+".xml.gz", gzipReport(t)),
		),
		attachmentMail("attachment without filename", attachmentPart("application/octet-stream", "", gzipReport(t))),
		attachmentMail("zip without extensions", attachmentPart("application/octet-stream", rfcName, zipFile(t, rfcName, loadReport(t)))),
		attachmentMail("gz without extensions", attachmentPart("application/octet-stream", "report", gzipReport(t))),
		attachmentMail("zip without extensions and invalid content", attachmentPart("application/octet-stream", "report", zipFile(t, "report", []byte("no xml")))),
		textMail("junk"),
	)

	sysLog := &fakeSyslog{}
	a := newTestApp(t, addr, sysLog, 100)
	hasMore, err := a.fetchIMAP(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasMore {
		t.Fatal("expected no more messages")
	}

	if got, want := sysLog.count(), 6*recordsCount; got != want {
		t.Fatalf("expected %d syslog entries, got %d", want, got)
	}
	if remaining := remainingSubjects(t, addr); len(remaining) != 0 {
		t.Fatalf("expected all messages to be deleted, remaining: %v", remaining)
	}
}

func TestFetchIMAPKeepsMessagesOnSyslogError(t *testing.T) {
	t.Parallel()

	addr := startIMAPServer(t)
	appendMessages(t, addr,
		reportMail(t, "report 1"),
		textMail("junk"),
		reportMail(t, "report 2"),
	)

	sysLog := &fakeSyslog{err: errors.New("connection refused")}
	a := newTestApp(t, addr, sysLog, 100)
	if _, err := a.fetchIMAP(t.Context()); err == nil {
		t.Fatal("expected an error when syslog fails")
	}

	// the default message of the memory backend is junk and processed before
	// the syslog error occurs, so it is deleted. Everything after the first
	// failure is kept for the next run.
	want := []string{"junk", "report 1", "report 2"}
	if got := remainingSubjects(t, addr); !slices.Equal(got, want) {
		t.Fatalf("expected remaining messages %v, got %v", want, got)
	}
}

func TestFetchIMAPCancelledContextKeepsMessages(t *testing.T) {
	t.Parallel()

	addr := startIMAPServer(t)
	appendMessages(t, addr, reportMail(t, "report"), textMail("junk"))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	sysLog := &fakeSyslog{}
	a := newTestApp(t, addr, sysLog, 100)
	if _, err := a.fetchIMAP(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sysLog.count() != 0 {
		t.Fatalf("expected no syslog entries, got %d", sysLog.count())
	}
	want := []string{defaultSubject, "junk", "report"}
	if got := remainingSubjects(t, addr); !slices.Equal(got, want) {
		t.Fatalf("expected remaining messages %v, got %v", want, got)
	}
}

func TestImapLoopProcessesAllBatches(t *testing.T) {
	t.Parallel()

	addr := startIMAPServer(t)
	appendMessages(t, addr,
		reportMail(t, "report 1"),
		reportMail(t, "report 2"),
		reportMail(t, "report 3"),
		textMail("spam"),
	)

	sysLog := &fakeSyslog{}
	a := newTestApp(t, addr, sysLog, 2)
	if err := a.imapLoop(t.Context()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := sysLog.count(), 3*recordsCount; got != want {
		t.Fatalf("expected %d syslog entries, got %d", want, got)
	}
	if remaining := remainingSubjects(t, addr); len(remaining) != 0 {
		t.Fatalf("expected all messages to be deleted, remaining: %v", remaining)
	}
}

func TestImapLoopDevModeStopsAfterFirstBatch(t *testing.T) {
	t.Parallel()

	addr := startIMAPServer(t)
	appendMessages(t, addr, reportMail(t, "report 1"), reportMail(t, "report 2"))

	a := newTestApp(t, addr, nil, 1)
	a.sysLog = nil
	a.devMode = true

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := a.imapLoop(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("imapLoop did not return in dev mode")
	}
	want := []string{defaultSubject, "report 1", "report 2"}
	if got := remainingSubjects(t, addr); !slices.Equal(got, want) {
		t.Fatalf("expected remaining messages %v, got %v", want, got)
	}
}

func TestConnectRefusesPlaintext(t *testing.T) {
	t.Parallel()

	addr := startIMAPServer(t)
	conf := config.IMAPConfig{
		Host:    addr,
		Folder:  testFolder,
		Timeout: config.Duration{Duration: 5 * time.Second},
	}
	logger := imapLogger{log: slog.New(slog.DiscardHandler)}
	if _, err := imap.Connect(conf, logger); !errors.Is(err, imap.ErrNoTLS) {
		t.Fatalf("expected ErrNoTLS, got %v", err)
	}

	conf.AllowPlaintext = true
	c, err := imap.Connect(conf, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = c.Logout()
}
