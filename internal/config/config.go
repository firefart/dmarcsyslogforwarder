package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/go-playground/validator/v10"
)

type Duration struct {
	time.Duration
}

func (d *Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	value, ok := v.(string)
	if !ok {
		// plain numbers would be interpreted as nanoseconds which is never what the user wants
		return fmt.Errorf("invalid duration %s: please use a string like \"10s\" or \"1h\"", string(b))
	}
	var err error
	d.Duration, err = time.ParseDuration(value)
	return err
}

type Configuration struct {
	Format            string     `json:"format" validate:"required,oneof=xml json"`
	SyslogServer      string     `json:"syslogServer" validate:"required,hostname_port"`
	SyslogProtocol    string     `json:"syslogProtocol" validate:"oneof='' tcp udp"`
	SyslogTag         string     `json:"syslogTag" validate:"required"`
	DNSServer         string     `json:"dnsServer"`
	DNSConnectTimeout Duration   `json:"dnsConnectTimeout" validate:"required"`
	DNSTimeout        Duration   `json:"dnsTimeout" validate:"required"`
	DNSCacheTimeout   Duration   `json:"dnsCacheTimeout" validate:"required"`
	FetchInterval     Duration   `json:"fetchInterval" validate:"required"`
	ImapConfig        IMAPConfig `json:"imap" validate:"required"`
	BatchSize         int        `json:"batchSize" validate:"required,gt=0"`
	EventID           string     `json:"eventID"`
	EventCategory     string     `json:"eventCategory"`
}

type IMAPConfig struct {
	Host           string   `json:"host" validate:"required,hostname_port"`
	SSL            bool     `json:"ssl"`
	User           string   `json:"user"`
	Pass           string   `json:"pass"` // nolint: gosec
	Folder         string   `json:"folder" validate:"required"`
	IgnoreCert     bool     `json:"ignoreCert"`
	AllowPlaintext bool     `json:"allowPlaintext"`
	Timeout        Duration `json:"timeout" validate:"required"`
}

func GetConfig(f string) (Configuration, error) {
	if f == "" {
		return Configuration{}, errors.New("please provide a valid config file")
	}

	// set some defaults
	defaults := Configuration{
		Format: "xml",
		FetchInterval: Duration{
			Duration: 1 * time.Hour,
		},
		BatchSize:      30,
		SyslogProtocol: "tcp",
		SyslogTag:      "dmarc",
		DNSConnectTimeout: Duration{
			Duration: 1 * time.Second,
		},
		DNSTimeout: Duration{
			Duration: 10 * time.Second,
		},
		DNSCacheTimeout: Duration{
			Duration: 1 * time.Hour,
		},
		EventID:       "",
		EventCategory: "",
	}

	b, err := os.ReadFile(f) // nolint: gosec
	if err != nil {
		return Configuration{}, err
	}
	reader := bytes.NewReader(b)

	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&defaults); err != nil {
		return Configuration{}, err
	}

	validate := validator.New(validator.WithRequiredStructEnabled())

	if err := validate.Struct(defaults); err != nil {
		if _, ok := errors.AsType[*validator.InvalidValidationError](err); ok {
			return Configuration{}, err
		}

		valErr, ok := errors.AsType[validator.ValidationErrors](err)
		if !ok {
			return Configuration{}, fmt.Errorf("could not cast err to ValidationErrors: %w", err)
		}
		errs := make([]error, 0, len(valErr))
		for _, err := range valErr {
			errs = append(errs, err)
		}
		return Configuration{}, errors.Join(errs...)
	}

	if err := validateDurations(defaults); err != nil {
		return Configuration{}, err
	}

	return defaults, nil
}

func validateDurations(c Configuration) error {
	durations := []struct {
		name  string
		value time.Duration
	}{
		{"dnsConnectTimeout", c.DNSConnectTimeout.Duration},
		{"dnsTimeout", c.DNSTimeout.Duration},
		{"dnsCacheTimeout", c.DNSCacheTimeout.Duration},
		{"fetchInterval", c.FetchInterval.Duration},
		{"imap.timeout", c.ImapConfig.Timeout.Duration},
	}
	var errs []error
	for _, d := range durations {
		if d.value <= 0 {
			errs = append(errs, fmt.Errorf("%s must be greater than 0, got %s", d.name, d.value))
		}
	}
	return errors.Join(errs...)
}
