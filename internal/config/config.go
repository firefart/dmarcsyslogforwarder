package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		d.Duration = time.Duration(value)
		return nil
	case string:
		var err error
		d.Duration, err = time.ParseDuration(value)
		if err != nil {
			return err
		}
		return nil
	default:
		return errors.New("invalid duration")
	}
}

type Configuration struct {
	Format            string     `json:"format"`
	SyslogServer      string     `json:"syslogServer"`
	SyslogProtocol    string     `json:"syslogProtocol"`
	SyslogTag         string     `json:"syslogTag"`
	DnsServer         string     `json:"dnsServer"`
	DnsConnectTimeout Duration   `json:"dnsConnectTimeout"`
	DnsTimeout        Duration   `json:"dnsTimeout"`
	DnsCacheTimeout   Duration   `json:"dnsCacheTimeout"`
	FetchInterval     Duration   `json:"fetchInterval"`
	ImapConfig        IMAPConfig `json:"imap"`
	BatchSize         int        `json:"batchSize"`
	EventID           string     `json:"eventID"`
	EventCategory     string     `json:"eventCategory"`
}

type IMAPConfig struct {
	Host       string   `json:"host"`
	SSL        bool     `json:"ssl"`
	User       string   `json:"user"`
	Pass       string   `json:"pass"`
	Folder     string   `json:"folder"`
	IgnoreCert bool     `json:"ignoreCert"`
	Timeout    Duration `json:"timeout"`
}

func GetConfig(defaults Configuration, f string) (*Configuration, error) {
	if f == "" {
		return nil, fmt.Errorf("please provide a valid config file")
	}

	b, err := os.ReadFile(f) // nolint: gosec
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(b)

	decoder := json.NewDecoder(reader)
	if err = decoder.Decode(&defaults); err != nil {
		return nil, err
	}

	if defaults.Format != "xml" && defaults.Format != "json" {
		return nil, fmt.Errorf("invalid format %s supplied", defaults.Format)
	}

	return &defaults, nil
}
