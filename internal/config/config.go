package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/hashicorp/go-multierror"
)

type Duration struct {
	time.Duration
}

func (d *Duration) MarshalJSON() ([]byte, error) {
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
	EventID           string     `json:"eventID" validate:"required"`
	EventCategory     string     `json:"eventCategory" validate:"required"`
}

type IMAPConfig struct {
	Host       string   `json:"host" validate:"required,hostname_port"`
	SSL        bool     `json:"ssl"`
	User       string   `json:"user"`
	Pass       string   `json:"pass"`
	Folder     string   `json:"folder" validate:"required"`
	IgnoreCert bool     `json:"ignoreCert"`
	Timeout    Duration `json:"timeout" validate:"required"`
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
		var invalidValidationError *validator.InvalidValidationError
		if errors.As(err, &invalidValidationError) {
			return Configuration{}, err
		}

		var valErr validator.ValidationErrors
		if ok := errors.As(err, &valErr); !ok {
			return Configuration{}, fmt.Errorf("could not cast err to ValidationErrors: %w", err)
		}
		var resultErr error
		for _, err := range valErr {
			resultErr = multierror.Append(resultErr, err)
		}
		return Configuration{}, resultErr
	}

	return defaults, nil
}
