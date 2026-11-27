package helper

import (
	"bytes"
)

// https://en.wikipedia.org/wiki/List_of_file_signatures
var (
	gzMagic  = [][]byte{{0x1f, 0x8b}}
	zipMagic = [][]byte{
		{0x50, 0x4b, 0x03, 0x04},
		{0x50, 0x4b, 0x05, 0x06},
		{0x50, 0x4b, 0x07, 0x08},
	}
)

func hasMagic(content []byte, magics [][]byte) bool {
	for _, magic := range magics {
		if bytes.HasPrefix(content, magic) {
			return true
		}
	}
	return false
}

// IsGZ checks if the content starts with the gzip magic bytes
func IsGZ(content []byte) bool {
	return hasMagic(content, gzMagic)
}

// IsZIP checks if the content starts with the zip magic bytes
func IsZIP(content []byte) bool {
	return hasMagic(content, zipMagic)
}

// IsSupportedArchive checks if the content is a gzip or zip archive
func IsSupportedArchive(content []byte) bool {
	return IsGZ(content) || IsZIP(content)
}
