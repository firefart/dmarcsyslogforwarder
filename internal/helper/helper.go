package helper

import (
	"bytes"
)

// https://en.wikipedia.org/wiki/List_of_file_signatures
var magicTable = [][]byte{
	{31, 139},      // .gz "\x1f\x8b"
	{80, 75, 3, 4}, // .zip "\x50\x4B\x03\x04"
	{80, 75, 5, 6}, // .zip "\x50\x4B\x05\x06"
	{80, 75, 7, 8}, // .zip "\x50\x4B\x07\x08"
}

func IsSupportedArchive(content []byte) bool {
	sliceEnd := 10
	if len(content) < sliceEnd {
		sliceEnd = len(content)
	}
	contentStr := content[0:sliceEnd]

	for _, magic := range magicTable {
		if bytes.HasPrefix(contentStr, magic) {
			return true
		}
	}

	return false
}
