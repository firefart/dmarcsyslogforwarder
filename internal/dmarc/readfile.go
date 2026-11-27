package dmarc

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/firefart/dmarcsyslogforwarder/internal/helper"
)

const xsTag = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="http://dmarc.org/dmarc-xml/0.1">`

// MaxReportSize is the maximum size of an uncompressed report. This protects
// against zip and gzip bombs as anyone can send mails to the report mailbox.
const MaxReportSize = 100 * 1024 * 1024 // 100 MiB

// ErrUnsupportedFile is returned when a file is neither an XML file nor a supported archive.
var ErrUnsupportedFile = errors.New("unsupported file type")

// readLimited reads at most limit bytes from r and returns an error if there is more data
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("uncompressed content exceeds the maximum size of %d bytes", limit)
	}
	return content, nil
}

// readGZ returns the uncompressed content and the optional original filename
// stored in the gzip header
func readGZ(content []byte) ([]byte, string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, "", fmt.Errorf("could not gzip read: %w", err)
	}
	defer gz.Close()

	xmlContent, err := readLimited(gz, MaxReportSize)
	if err != nil {
		return nil, "", fmt.Errorf("could not read: %w", err)
	}
	return xmlContent, gz.Name, nil
}

func readZIP(content []byte) ([]byte, string, error) {
	r, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, "", fmt.Errorf("could not open zip: %w", err)
	}
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// only use first file in the zip file
		xmlContent, err := readZIPFile(f)
		if err != nil {
			return nil, "", err
		}
		return xmlContent, f.FileInfo().Name(), nil
	}
	return nil, "", errors.New("no valid file found within zip archive")
}

func readZIPFile(f *zip.File) ([]byte, error) {
	x, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("could not open file %s inside zip: %w", f.Name, err)
	}
	defer x.Close()
	xmlContent, err := readLimited(x, MaxReportSize)
	if err != nil {
		return nil, fmt.Errorf("could not read file %s inside zip: %w", f.Name, err)
	}
	return xmlContent, nil
}

type fileType int

const (
	fileTypeUnknown fileType = iota
	fileTypeXML
	fileTypeGZ
	fileTypeZIP
)

// fileTypeFromExtension returns the file type based on the extension of the
// filename. Filenames without an extension and filenames following the RFC
// naming scheme without an extension (where filepath.Ext returns everything
// after the dot of the domain) both return fileTypeUnknown.
func fileTypeFromExtension(filename string) fileType {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".xml":
		return fileTypeXML
	case ".gz", ".gzip":
		return fileTypeGZ
	case ".zip":
		return fileTypeZIP
	default:
		return fileTypeUnknown
	}
}

// detectFileType determines the file type by the file extension and falls
// back to the magic bytes of the content if there is no known extension
func detectFileType(filename string, content []byte) fileType {
	if t := fileTypeFromExtension(filename); t != fileTypeUnknown {
		return t
	}

	switch {
	case helper.IsGZ(content):
		return fileTypeGZ
	case helper.IsZIP(content):
		return fileTypeZIP
	default:
		return fileTypeUnknown
	}
}

// ReadFile parses a dmarc report. The filename is used to determine the file
// type and the returned filename is the name of the xml file which can be
// used to determine the reporting domain.
// ErrUnsupportedFile is returned if the file is not a dmarc report.
func ReadFile(filename string, content []byte) (string, *XMLReport, error) {
	var xmlContent []byte
	var xmlFilename string
	var err error
	switch detectFileType(filename, content) {
	case fileTypeXML:
		xmlContent = content
		xmlFilename = filename
	case fileTypeGZ:
		var gzName string
		xmlContent, gzName, err = readGZ(content)
		if err != nil {
			return "", nil, err
		}
		switch {
		case fileTypeFromExtension(filename) == fileTypeGZ:
			xmlFilename = strings.TrimSuffix(filename, filepath.Ext(filename))
		case gzName != "":
			// the attachment has no extension, use the original filename from the gzip header
			xmlFilename = gzName
		default:
			xmlFilename = filename
		}
	case fileTypeZIP:
		xmlContent, xmlFilename, err = readZIP(content)
		if err != nil {
			return "", nil, err
		}
	case fileTypeUnknown:
		return "", nil, fmt.Errorf("%w: %q", ErrUnsupportedFile, filename)
	}
	// some xmls contain invalid XML by adding an unclosed xs tag
	xmlContent = bytes.ReplaceAll(xmlContent, []byte(xsTag), []byte(""))

	// parse XML into object
	var xmlDocument XMLReport
	if err := xml.Unmarshal(xmlContent, &xmlDocument); err != nil {
		// the extracted file has no xml extension, so we only tried to
		// parse it as xml. If that fails it is not a supported file.
		if fileTypeFromExtension(xmlFilename) != fileTypeXML {
			return "", nil, fmt.Errorf("%w: %q is not a valid xml file: %w", ErrUnsupportedFile, xmlFilename, err)
		}
		return "", nil, fmt.Errorf("error on xml unmarshal: %w", err)
	}

	return xmlFilename, &xmlDocument, nil
}
