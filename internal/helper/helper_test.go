package helper

import "testing"

func TestIsSupportedArchive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content []byte
		gz      bool
		zip     bool
	}{
		{name: "empty", content: nil},
		{name: "short", content: []byte{0x1f}},
		{name: "gz", content: []byte{0x1f, 0x8b, 0x08, 0x00}, gz: true},
		{name: "zip local file header", content: []byte("PK\x03\x04rest"), zip: true},
		{name: "zip empty archive", content: []byte("PK\x05\x06rest"), zip: true},
		{name: "zip spanned", content: []byte("PK\x07\x08rest"), zip: true},
		{name: "xml", content: []byte("<?xml version=\"1.0\"?>")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsGZ(tt.content); got != tt.gz {
				t.Errorf("IsGZ = %t, want %t", got, tt.gz)
			}
			if got := IsZIP(tt.content); got != tt.zip {
				t.Errorf("IsZIP = %t, want %t", got, tt.zip)
			}
			if got := IsSupportedArchive(tt.content); got != (tt.gz || tt.zip) {
				t.Errorf("IsSupportedArchive = %t, want %t", got, tt.gz || tt.zip)
			}
		})
	}
}
