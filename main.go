package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"log/syslog"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/firefart/dmarcsyslogforwarder/internal/config"
	"github.com/firefart/dmarcsyslogforwarder/internal/dmarc"
	"github.com/firefart/dmarcsyslogforwarder/internal/dns"
	"github.com/firefart/dmarcsyslogforwarder/internal/helper"
	"github.com/firefart/dmarcsyslogforwarder/internal/imap"

	goimap "github.com/emersion/go-imap"
	"github.com/emersion/go-message/mail"

	// needed to handle other charsets too
	_ "github.com/emersion/go-message/charset"
)

// errSyslog is returned when a syslog entry could not be sent. Messages
// failing with this error are kept on the server so they are retried later.
var errSyslog = errors.New("could not send syslog entry")

type app struct {
	// sysLog is nil in dev mode
	sysLog    io.Writer
	dns       *dns.CachedDNSResolver
	config    config.Configuration
	devMode   bool
	debugMode bool
	log       *slog.Logger
}

func main() {
	debugMode := flag.Bool("debug", false, "Print debug output")
	jsonOutput := flag.Bool("json", false, "output in json instead")
	devMode := flag.Bool("devmode", false, "enable dev mode (no syslog, no message delete and goroutine printing)")
	configFile := flag.String("config", "", "Config File to use")
	configCheckMode := flag.Bool("configcheck", false, "just check the config")
	version := flag.Bool("version", false, "show version")
	flag.Parse()

	if *version {
		buildInfo, ok := debug.ReadBuildInfo()
		if !ok {
			fmt.Println("Unable to determine version information") // nolint:forbidigo
			os.Exit(1)
		}
		fmt.Printf("%s", buildInfo) // nolint:forbidigo
		os.Exit(0)
	}

	logger := newLogger(*debugMode, *jsonOutput)

	if err := start(logger, *configFile, *configCheckMode, *devMode, *debugMode); err != nil {
		logError(logger, err)
		os.Exit(1)
	}
}

type multiError interface {
	error
	Unwrap() []error
}

// logError logs every error of a joined error on its own line
func logError(logger *slog.Logger, err error) {
	if merr, ok := errors.AsType[multiError](err); ok {
		for _, e := range merr.Unwrap() {
			logger.Error(e.Error())
		}
		return
	}
	logger.Error(err.Error())
}

func start(logger *slog.Logger, configFile string, configCheckMode, devMode, debugMode bool) error {
	settings, err := config.GetConfig(configFile)
	if err != nil {
		logger.Error("could not read config", slog.String("filename", configFile))
		return err
	}

	if configCheckMode {
		logger.Info("config is valid", slog.String("filename", configFile))
		return nil
	}

	// SIGTERM is sent by systemd and docker on stop
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		// restore the default signal behaviour so a second signal kills the program immediately
		stop()
		logger.Info("shutting down, finishing current message", slog.Any("reason", context.Cause(ctx)))
	}()

	return run(ctx, settings, logger, devMode, debugMode)
}

func run(ctx context.Context, settings config.Configuration, logger *slog.Logger, devMode bool, debugMode bool) error {
	var sysLog io.Writer
	if !devMode {
		w, err := syslog.Dial(settings.SyslogProtocol, settings.SyslogServer,
			syslog.LOG_WARNING|syslog.LOG_DAEMON, settings.SyslogTag)
		if err != nil {
			return fmt.Errorf("could not connect to syslog server: %w", err)
		}
		defer w.Close()
		sysLog = w
	}

	dnsResolver := dns.NewCachedDNSResolver(ctx, settings.DNSServer, settings.DNSConnectTimeout.Duration, settings.DNSTimeout.Duration, settings.DNSCacheTimeout.Duration, logger)

	app := app{
		sysLog:    sysLog,
		dns:       dnsResolver,
		config:    settings,
		devMode:   devMode,
		log:       logger,
		debugMode: debugMode,
	}

	// print number of goroutines in devmode
	if devMode {
		go func() {
			goRoutineTicker := time.NewTicker(3 * time.Second)
			defer goRoutineTicker.Stop()
			for {
				select {
				case <-goRoutineTicker.C:
					app.log.Debug("goroutines", slog.Int("count", runtime.NumGoroutine()))
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// used to start the ticker immediately
	// otherwise it first runs after the first
	// period
	app.runOnce(ctx)

	ticker := time.NewTicker(settings.FetchInterval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			app.log.Info("context done")
			return nil
		case <-ticker.C:
			app.runOnce(ctx)
		}
	}
}

func (a *app) runOnce(ctx context.Context) {
	a.log.Info("starting new run")
	if err := a.imapLoop(ctx); err != nil {
		// only log the error here, so we keep the loop running
		a.log.Error("Received error", slog.String("err", err.Error()))
	}
	a.log.Info("run finished")
}

// run in batch sizes as some IMAP servers have pretty
// short timeouts and the imap library does not handle
// reconnects
func (a *app) imapLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			// we are shutting down, this is not an error
			return nil
		default:
		}
		a.log.Debug("starting new imap loop", slog.Int("batch-size", a.config.BatchSize))
		hasMore, err := a.fetchIMAP(ctx)
		if err != nil {
			return err
		}
		if !hasMore {
			return nil
		}
		if a.devMode {
			// messages are not deleted in dev mode so we would fetch the same batch over and over again
			a.log.Info("dev mode: messages are not deleted, stopping after the first batch")
			return nil
		}
	}
}

type imapLogger struct {
	log *slog.Logger
}

func (l imapLogger) Printf(format string, v ...any) {
	l.log.Error(fmt.Sprintf(format, v...))
}

func (l imapLogger) Println(v ...any) {
	l.log.Error(strings.TrimSpace(fmt.Sprintln(v...)))
}

// messageOutcome describes what should happen with a message after processing it
type messageOutcome int

const (
	// outcomeForwarded means the message contained a dmarc report that was sent to syslog
	outcomeForwarded messageOutcome = iota
	// outcomeInvalid means the message did not contain a valid dmarc report
	outcomeInvalid
	// outcomeRetry means the message could not be processed because of a
	// temporary error. It is kept on the server and retried on the next run.
	outcomeRetry
)

func (a *app) fetchIMAP(ctx context.Context) (bool, error) {
	imapLog := imapLogger{log: a.log}
	c, err := imap.Connect(a.config.ImapConfig, imapLog)
	if err != nil {
		return false, fmt.Errorf("could not connect to %s: %w", a.config.ImapConfig.Host, err)
	}
	defer c.Close()

	a.log.Debug("connected to imap server")

	// also log IMAP messages in debug mode
	if a.debugMode {
		c.SetDebug(os.Stdout)
	}

	if err := c.Login(a.config.ImapConfig.User, a.config.ImapConfig.Pass); err != nil {
		return false, fmt.Errorf("could not login: %w", err)
	}

	a.log.Debug("successful login")

	defer c.Logout() // nolint: errcheck

	hasFolder, err := imap.HasImapFolder(c, a.config.ImapConfig.Folder)
	if err != nil {
		return false, fmt.Errorf("could not check if folder %s exists: %w", a.config.ImapConfig.Folder, err)
	}

	if !hasFolder {
		return false, fmt.Errorf("imap folder %s not found in account", a.config.ImapConfig.Folder)
	}

	mbox, err := c.Select(a.config.ImapConfig.Folder, false)
	if err != nil {
		return false, fmt.Errorf("could not select folder %s: %w", a.config.ImapConfig.Folder, err)
	}

	a.log.Info("Opened mailbox",
		slog.String("mailbox", mbox.Name),
		slog.Int("message-count", int(mbox.Messages)),
		slog.Int("unread-count", int(mbox.Unseen)),
	)

	criteria := goimap.NewSearchCriteria()
	criteria.WithoutFlags = []string{goimap.DeletedFlag}
	ids, err := c.Search(criteria)
	if err != nil {
		return false, fmt.Errorf("could not search for mails: %w", err)
	}

	a.log.Debug("found mails without the DELETED flag", slog.Int("count", len(ids)))

	if len(ids) == 0 {
		// no mails to process
		return false, nil
	}

	batch := ids[:min(a.config.BatchSize, len(ids))]
	hasMore := len(ids) > len(batch)
	seqset := new(goimap.SeqSet)
	seqset.AddNum(batch...)

	a.log.Debug("Fetching messages", slog.String("messages", seqset.String()))

	messages := make(chan *goimap.Message, 10)
	done := make(chan error, 1)

	// Get the whole message body
	section := &goimap.BodySectionName{}
	items := []goimap.FetchItem{
		section.FetchItem(),
		goimap.FetchBodyStructure,
		goimap.FetchEnvelope,
		goimap.FetchFlags,
		goimap.FetchInternalDate,
		goimap.FetchUid,
	}
	go func() {
		done <- c.Fetch(seqset, items, messages)
	}()

	var toDelete []uint32
	processed := 0
	retry := 0
	syslogFailed := false
	// the channel must always be drained completely so the fetch command can finish
	for msg := range messages {
		if ctx.Err() != nil || syslogFailed {
			// do not process the remaining messages, they stay on the server
			// and will be processed on the next run
			retry++
			continue
		}
		processed++
		switch a.handleMessage(ctx, msg) {
		case outcomeForwarded, outcomeInvalid:
			// also delete invalid messages to clean up junk
			toDelete = append(toDelete, msg.Uid)
		case outcomeRetry:
			retry++
			// if syslog is not reachable all following messages will also fail
			syslogFailed = ctx.Err() == nil
		}
	}

	a.log.Debug("waiting for fetch to finish")

	if err := <-done; err != nil {
		return false, fmt.Errorf("error on fetch: %w", err)
	}

	if !a.devMode && len(toDelete) > 0 {
		a.log.Info("Marking messages as deleted", slog.Int("count", len(toDelete)))
		if err := imap.MarkMessagesAsDeleted(c, toDelete); err != nil {
			return false, fmt.Errorf("could not mark messages as deleted: %w", err)
		}

		a.log.Info("Running expunge command (delete all marked messages)")
		if err := c.Expunge(nil); err != nil {
			return false, fmt.Errorf("could not expunge: %w", err)
		}
	}

	a.log.Info("Processed emails",
		slog.Int("processed", processed),
		slog.Int("deleted", len(toDelete)),
		slog.Int("kept-for-retry", retry),
	)

	if ctx.Err() != nil {
		return false, nil
	}

	if syslogFailed {
		// stop here, otherwise we would fetch the same messages over and over again
		return false, fmt.Errorf("%d message(s) could not be forwarded and will be retried on the next run", retry)
	}

	return hasMore, nil
}

func (a *app) handleMessage(ctx context.Context, msg *goimap.Message) messageOutcome {
	subject := ""
	if msg.Envelope != nil {
		subject = msg.Envelope.Subject
	}
	logger := a.log.With(slog.String("subject", subject), slog.Int("uid", int(msg.Uid)))
	logger.Info("Processing email")

	r := msg.GetBody(&goimap.BodySectionName{})
	if r == nil {
		logger.Error("server didn't return message body, marking it for deletion")
		return outcomeInvalid
	}
	logger.Debug("body length", slog.Int("len", r.Len()))

	valid, err := a.processMessage(ctx, r)
	if err != nil {
		if errors.Is(err, errSyslog) || ctx.Err() != nil {
			// reports that were already sent before the error will be sent again on the next run.
			// We accept the duplicates instead of losing reports.
			logger.Error("could not process message, keeping it for the next run", slog.String("err", err.Error()))
			return outcomeRetry
		}
		logger.Error("could not process message", slog.String("err", err.Error()))
	}

	if valid {
		logger.Debug("adding message to delete set")
		return outcomeForwarded
	}
	logger.Info("Message does not seem to be a valid dmarc report. Marking it for deletion.")
	return outcomeInvalid
}

// processMessage parses the email and sends all contained dmarc reports to syslog.
// It returns true if at least one report was found and sent.
func (a *app) processMessage(ctx context.Context, r io.Reader) (bool, error) {
	// indicates if the email is a valid dmarc report
	validDmarcReport := false
	m, err := mail.CreateReader(r)
	if err != nil {
		return false, fmt.Errorf("could not create reader: %w", err)
	}
	defer m.Close()
	a.log.Debug("reader created")

	for {
		if err := ctx.Err(); err != nil {
			return validDmarcReport, err
		}

		p, err := m.NextPart()
		if errors.Is(err, io.EOF) {
			a.log.Debug("EOF")
			return validDmarcReport, nil
		} else if err != nil {
			return validDmarcReport, fmt.Errorf("could not get next part: %w", err)
		}

		a.log.Debug("processing next part")

		var filename string
		var body []byte
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			a.log.Debug("inline header")
			// This is the message's text (can be plain-text or HTML)
			body, err = io.ReadAll(p.Body)
			if err != nil {
				return validDmarcReport, fmt.Errorf("could not read inline body: %w", err)
			}

			// sometimes the attachment is inlined so we check the magic bytes
			if !helper.IsSupportedArchive(body) {
				a.log.Debug("message", slog.String("content", string(body)))
				continue
			}
			a.log.Info("found inline attachment")
			filename = inlineFilename(h)
		case *mail.AttachmentHeader:
			mailHeader := m.Header
			a.log.Debug("attachment header",
				slog.String("date", mailHeader.Get("Date")),
				slog.String("from", mailHeader.Get("From")),
				slog.String("to", mailHeader.Get("To")),
				slog.String("subject", mailHeader.Get("Subject")),
			)
			// This is an attachment. If we can not determine the filename
			// the file type is detected by the content.
			filename, err = h.Filename()
			if err != nil {
				a.log.Debug("could not get attachment filename", slog.String("err", err.Error()))
			}

			body, err = io.ReadAll(p.Body)
			if err != nil {
				return validDmarcReport, fmt.Errorf("could not read attachment: %w", err)
			}
		default:
			a.log.Info("header type not implemented", slog.String("header", fmt.Sprintf("%v", p.Header)))
			continue
		}

		if err := a.sendAttachment(ctx, filename, body); err != nil {
			switch {
			case errors.Is(err, errSyslog), ctx.Err() != nil:
				return validDmarcReport, err
			case errors.Is(err, dmarc.ErrUnsupportedFile):
				// for example signatures or images
				a.log.Info("skipping attachment that is not a dmarc report", slog.String("filename", filename))
			default:
				a.log.Error("could not parse attachment", slog.String("filename", filename), slog.String("err", err.Error()))
			}
			continue
		}
		// we parsed and sent the attachment so it's valid
		validDmarcReport = true
	}
}

// inlineFilename returns the filename of an inline part
func inlineFilename(h *mail.InlineHeader) string {
	if _, params, err := h.ContentDisposition(); err == nil && params["filename"] != "" {
		return params["filename"]
	}
	if _, params, err := h.ContentType(); err == nil {
		return params["name"]
	}
	return ""
}

func (a *app) sendAttachment(ctx context.Context, filename string, body []byte) error {
	a.log.Info("Got attachment", slog.String("filename", filename))
	xmlFilename, xmlReport, err := dmarc.ReadFile(filename, body)
	if err != nil {
		return fmt.Errorf("could not read file %q: %w", filename, err)
	}

	var r [][]byte
	switch a.config.Format {
	case "xml":
		r, err = dmarc.ConvertToSyslogXML(xmlFilename, *xmlReport, a.dns, a.config.EventID, a.config.EventCategory)
		if err != nil {
			return fmt.Errorf("could not convert XML: %w", err)
		}
	case "json":
		r, err = dmarc.ConvertToSyslogJSON(xmlFilename, *xmlReport, a.dns, a.config.EventID, a.config.EventCategory)
		if err != nil {
			return fmt.Errorf("could not convert JSON: %w", err)
		}
	default:
		return fmt.Errorf("invalid format %s", a.config.Format)
	}

	// DNS lookups fail when the context is cancelled, so do not send
	// incomplete entries and retry the message on the next run
	if err := ctx.Err(); err != nil {
		return err
	}

	for _, report := range r {
		a.log.Debug("Converted entry", slog.String("report", string(report)))

		if a.sysLog == nil {
			continue
		}
		// hint: we can't check the number returned here because
		// it's just the len of the input, so pretty useless
		if _, err := a.sysLog.Write(report); err != nil {
			return fmt.Errorf("%w: %w", errSyslog, err)
		}
		a.log.Debug("wrote message to syslog")
	}

	return nil
}
