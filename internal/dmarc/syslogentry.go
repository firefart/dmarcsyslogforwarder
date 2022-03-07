package dmarc

import (
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/firefart/dmarcsyslogforwarder/internal/dns"
)

type SyslogEntry struct {
	XMLName          xml.Name         `xml:"syslog_entry"` // for xml serialisation
	Version          string           `xml:"version"`
	Domain           string           `xml:"domain"`
	DateBegin        int64            `xml:"date_begin"`
	DateEnd          int64            `xml:"date_end"`
	ReportID         string           `xml:"report_id"`
	OrgName          string           `xml:"org_name"`
	Email            string           `xml:"email"`
	ExtraContactInfo string           `xml:"extra_contact_info"`
	Errors           SyslogEntryError `xml:"errors"`
	SourceIP         string           `xml:"source_ip"`
	SourceDNS        SyslogEntryDNS   `xml:"source_dns"`
	SourceDNSString  string           `xml:"source_dns_string"`
	Count            int              `xml:"count"`
	EnvelopeTo       string           `xml:"envelope_to"`
	HeaderFrom       string           `xml:"header_from"`
	EnvelopeFrom     string           `xml:"envelope_from"`
	PolicyPublished  struct {
		Domain string `xml:"domain"`
		Adkim  string `xml:"adkim"`
		Aspf   string `xml:"aspf"`
		P      string `xml:"p"`
		Sp     string `xml:"sp"`
		Pct    string `xml:"pct"`
		Fo     string `xml:"fo" `
	} `xml:"policy_published"`
	PolicyEvaluated struct {
		Disposition string                 `xml:"disposition"`
		Dkim        string                 `xml:"dkim"`
		Spf         string                 `xml:"spf"`
		Reason      []PolicyOverrideReason `xml:"reason"`
	} `xml:"policy_evaluated"`
	ResultSpf struct {
		Domain string `xml:"domain"`
		Scope  string `xml:"scope"`
		Result string `xml:"result"`
	} `xml:"result_spf"`
	ResultDkim struct {
		Domain      string `xml:"domain"`
		Selector    string `xml:"selector"`
		Result      string `xml:"result"`
		HumanResult string `xml:"human_result"`
	} `xml:"result_dkim"`
}

type SyslogEntryError struct {
	Error []string `xml:"error"`
}

type SyslogEntryDNS struct {
	Domains []string `xml:"dns"`
}

func ConvertXMLToSyslog(filename string, report XMLReport, dns *dns.CachedDNSResolver) ([]SyslogEntry, error) {
	reportingDomain, err := getDomainFromFilename(filename)
	if err != nil {
		return nil, err
	}
	syslogs := make([]SyslogEntry, len(report.Records))
	for i, record := range report.Records {
		domains, err := dns.CachedDNSLookup(record.Row.SourceIP)
		if err != nil {
			domains = []string{}
		}

		syslog := SyslogEntry{
			Version:          report.Version,
			Domain:           reportingDomain,
			DateBegin:        report.ReportMetadata.DateRange.Begin,
			DateEnd:          report.ReportMetadata.DateRange.End,
			ReportID:         report.ReportMetadata.ReportID,
			OrgName:          report.ReportMetadata.OrgName,
			Email:            report.ReportMetadata.Email,
			ExtraContactInfo: report.ReportMetadata.ExtraContactInfo,
			Errors:           SyslogEntryError{Error: report.ReportMetadata.Error},
			SourceIP:         record.Row.SourceIP,
			SourceDNS:        SyslogEntryDNS{Domains: domains},
			SourceDNSString:  strings.Join(domains, ", "),
			Count:            record.Row.Count,
			EnvelopeTo:       record.Identifiers.EnvelopeTo,
			EnvelopeFrom:     record.Identifiers.EnvelopeFrom,
			HeaderFrom:       record.Identifiers.HeaderFrom,
			PolicyPublished:  report.PolicyPublished,
			PolicyEvaluated:  record.Row.PolicyEvaluated,
			ResultSpf:        record.AuthResults.Spf,
			ResultDkim:       record.AuthResults.Dkim,
		}
		syslogs[i] = syslog
	}
	return syslogs, nil
}

func getDomainFromFilename(filename string) (string, error) {
	// filename = receiver "!" policy-domain "!" begin-timestamp
	//               "!" end-timestamp [ "!" unique-id ] "." extension
	filename = filepath.Base(filename)
	parts := strings.Split(filename, "!")
	if len(parts) < 4 {
		return "", fmt.Errorf("filename %q does not match RFC", filename)
	}
	return parts[0], nil
}
