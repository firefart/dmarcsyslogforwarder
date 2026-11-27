package config

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"
)

func TestGetConfig(t *testing.T) {
	_, err := GetConfig(path.Join("..", "..", "testdata", "test.json"))
	if err != nil {
		t.Fatalf("got error when reading config file: %v", err)
	}
}

func TestGetConfigErrors(t *testing.T) {
	_, err := GetConfig("")
	if err == nil {
		t.Fatal("expected error on empty filename")
	}
	_, err = GetConfig("this_does_not_exist")
	if err == nil {
		t.Fatal("expected error on invalid file")
	}
}

func TestGetConfigInvalid(t *testing.T) {
	_, err := GetConfig(path.Join("..", "..", "testdata", "invalid.json"))
	if err == nil {
		t.Fatal("expected error when reading config file but got none")
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	f := path.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

const validConfigTemplate = `{
  "syslogServer": "localhost:514",
  "fetchInterval": %s,
  "imap": {
    "host": "localhost:993",
    "folder": "INBOX",
    "timeout": "3s"
  }
}`

func TestGetConfigDurations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		interval string
		valid    bool
	}{
		{name: "string", interval: `"1h"`, valid: true},
		{name: "number", interval: `3600`, valid: false},
		{name: "zero", interval: `"0s"`, valid: false},
		{name: "negative", interval: `"-1h"`, valid: false},
		{name: "garbage", interval: `"asdf"`, valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := writeConfig(t, fmt.Sprintf(validConfigTemplate, tt.interval))
			c, err := GetConfig(f)
			if tt.valid {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if c.FetchInterval.Duration != time.Hour {
					t.Fatalf("wrong interval: %s", c.FetchInterval.Duration)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})
	}
}
