package dmarc

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func loadTestReport(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "report.xml"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func gzipBytes(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipBytes(t *testing.T, name string, content []byte) []byte {
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

func TestReadFile(t *testing.T) {
	t.Parallel()

	report := loadTestReport(t)
	const rfcName = "google.com!example.com!1636416000!1636502399"

	tests := []struct {
		name             string
		filename         string
		content          []byte
		expectedFilename string
		expectedErr      error
		wantErr          bool
	}{
		{name: "xml", filename: rfcName + ".xml", content: report, expectedFilename: rfcName + ".xml"},
		{name: "uppercase xml", filename: rfcName + ".XML", content: report, expectedFilename: rfcName + ".XML"},
		{name: "gz", filename: rfcName + ".xml.gz", content: gzipBytes(t, report), expectedFilename: rfcName + ".xml"},
		{name: "gz name ending in g and z", filename: rfcName + "!xyzgg.gz", content: gzipBytes(t, report), expectedFilename: rfcName + "!xyzgg"},
		{name: "uppercase gz", filename: rfcName + ".xml.GZ", content: gzipBytes(t, report), expectedFilename: rfcName + ".xml"},
		{name: "zip", filename: rfcName + ".zip", content: zipBytes(t, rfcName+".xml", report), expectedFilename: rfcName + ".xml"},
		{name: "gz detected by magic bytes", filename: "report.bin", content: gzipBytes(t, report), expectedFilename: "report.bin"},
		{name: "zip detected by magic bytes", filename: "", content: zipBytes(t, rfcName+".xml", report), expectedFilename: rfcName + ".xml"},
		{name: "unsupported file", filename: "smime.p7s", content: []byte("signature"), expectedErr: ErrUnsupportedFile, wantErr: true},
		{name: "invalid xml", filename: "report.xml", content: []byte("<feedback>"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			filename, xmlReport, err := ReadFile(tt.filename, tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error but got none")
				}
				if tt.expectedErr != nil && !errors.Is(err, tt.expectedErr) {
					t.Fatalf("expected error %v, got %v", tt.expectedErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if filename != tt.expectedFilename {
				t.Fatalf("expected filename %q, got %q", tt.expectedFilename, filename)
			}
			if len(xmlReport.Records) != 2 {
				t.Fatalf("expected 2 records, got %d", len(xmlReport.Records))
			}
		})
	}
}

func TestReadLimited(t *testing.T) {
	t.Parallel()

	compressed := gzipBytes(t, make([]byte, 10*1024))
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readLimited(gz, 1024); err == nil {
		t.Fatal("expected an error on oversized content")
	}

	gz, err = gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	b, err := readLimited(gz, 10*1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(b) != 10*1024 {
		t.Fatalf("expected %d bytes, got %d", 10*1024, len(b))
	}
}

func TestGetReportingDomain(t *testing.T) {
	t.Parallel()

	var report XMLReport
	report.ReportMetadata.Email = "noreply@reporter.example"
	report.ReportMetadata.OrgName = "Reporter Inc"

	if got := getReportingDomain("google.com!example.com!1!2.xml", report); got != "google.com" {
		t.Fatalf("expected domain from filename, got %q", got)
	}
	if got := getReportingDomain("report.xml", report); got != "reporter.example" {
		t.Fatalf("expected domain from email, got %q", got)
	}
	report.ReportMetadata.Email = ""
	if got := getReportingDomain("report.xml", report); got != "Reporter Inc" {
		t.Fatalf("expected org name, got %q", got)
	}
}
