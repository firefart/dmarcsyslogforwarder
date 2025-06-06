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
)

const xsTag = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="http://dmarc.org/dmarc-xml/0.1">`

func readGZ(content []byte) ([]byte, error) {
	buf := bytes.NewBuffer(content)
	gz, err := gzip.NewReader(buf)
	if err != nil {
		return nil, fmt.Errorf("could not gzip read: %w", err)
	}
	defer gz.Close()

	xmlContent, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("could not read: %w", err)
	}
	return xmlContent, nil
}

func readZIP(content []byte) ([]byte, string, error) {
	buf := bytes.NewReader(content)
	r, err := zip.NewReader(buf, int64(len(content)))
	if err != nil {
		return nil, "", fmt.Errorf("could not open zip: %w", err)
	}
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		x, err := f.Open()
		if err != nil {
			return nil, "", fmt.Errorf("could not open file %s inside zip: %w", f.Name, err)
		}
		xmlContent, err := io.ReadAll(x)
		if err != nil {
			return nil, "", fmt.Errorf("could not read file %s inside zip: %w", f.Name, err)
		}
		// only use first file in the zip file
		return xmlContent, f.FileInfo().Name(), nil
	}
	return nil, "", errors.New("no valid file found within zip archive")
}

func ReadFile(filename string, content []byte) (string, *XMLReport, error) {
	var xmlContent []byte
	var xmlFilename string
	var err error
	ext := filepath.Ext(filename)
	switch ext {
	case ".xml":
		xmlContent = content
		xmlFilename = filename
	case ".gz":
		xmlContent, err = readGZ(content)
		if err != nil {
			return "", nil, err
		}
		xmlFilename = strings.TrimRight(filename, ".gz")
	case ".zip":
		xmlContent, xmlFilename, err = readZIP(content)
		if err != nil {
			return "", nil, err
		}
	default:
		return "", nil, fmt.Errorf("unknown extension %s", ext)
	}
	// some xmls contain invalid XML by adding an unclosed xs tag
	xmlContent = bytes.ReplaceAll(xmlContent, []byte(xsTag), []byte(""))

	// parse XML into object
	var xmlDocument XMLReport
	if err := xml.Unmarshal(xmlContent, &xmlDocument); err != nil {
		return "", nil, fmt.Errorf("error on xml unmarshal: %w", err)
	}

	return xmlFilename, &xmlDocument, nil
}
