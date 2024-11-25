package dmarc

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/firefart/dmarcsyslogforwarder/internal/dns"
)

type CustomTime time.Time

func (t CustomTime) MarshalJSON() ([]byte, error) {
	stamp := fmt.Sprintf("\"%s\"", time.Time(t).Format(time.RFC822Z))
	return []byte(stamp), nil
}

func (t CustomTime) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	stamp := time.Time(t).Format(time.RFC822Z)
	return e.EncodeElement(stamp, start)
}

type SyslogEntry struct {
	XMLName          xml.Name              `xml:"syslog_entry" json:"-"`                                    // for xml serialisation
	EventID          string                `xml:"event_id,omitempty" json:"event_id,omitempty"`             // SIEM specific
	EventCategory    string                `xml:"event_category,omitempty" json:"event_category,omitempty"` // SIEM specific
	Version          string                `xml:"version" json:"version"`
	Domain           string                `xml:"domain" json:"domain"`
	DateBegin        int64                 `xml:"date_begin" json:"date_begin"`
	DateEnd          int64                 `xml:"date_end" json:"date_end"`
	DateBeginParsed  CustomTime            `xml:"date_begin_parsed" json:"date_begin_parsed"`
	DateEndParsed    CustomTime            `xml:"date_end_parsed" json:"date_end_parsed"`
	ReportID         string                `xml:"report_id" json:"report_id"`
	OrgName          string                `xml:"org_name" json:"org_name"`
	Email            string                `xml:"email" json:"email"`
	ExtraContactInfo string                `xml:"extra_contact_info" json:"extra_contact_info"`
	Errors           []string              `xml:"errors>error" json:"errors"`
	SourceIP         string                `xml:"source_ip" json:"source_ip"`
	SourceDNS        []string              `xml:"source_dns>dns" json:"source_dns"`
	SourceDNSString  string                `xml:"source_dns_string" json:"source_dns_string"`
	Count            int                   `xml:"count" json:"count"`
	EnvelopeTo       string                `xml:"envelope_to" json:"envelope_to"`
	HeaderFrom       string                `xml:"header_from" json:"header_from"`
	EnvelopeFrom     string                `xml:"envelope_from" json:"envelope_from"`
	PolicyPublished  SyslogPolicyPublished `xml:"policy_published" json:"policy_published"`
	PolicyEvaluated  SyslogPolicyEvaluated `xml:"policy_evaluated" json:"policy_evaluated"`
	ResultSpf        SyslogResultSPF       `xml:"result_spf" json:"result_spf"`
	ResultDkim       SyslogResultDKIM      `xml:"result_dkim" json:"result_dkim"`
}

type SyslogPolicyPublished struct {
	Domain string `xml:"domain" json:"domain"`
	Adkim  string `xml:"adkim" json:"adkim"`
	Aspf   string `xml:"aspf" json:"aspf"`
	P      string `xml:"p" json:"p"`
	Sp     string `xml:"sp" json:"sp"`
	Pct    string `xml:"pct" json:"pct"`
	Fo     string `xml:"fo" json:"fo"`
}

type SyslogPolicyEvaluated struct {
	Disposition string                       `xml:"disposition" json:"disposition"`
	Dkim        string                       `xml:"dkim" json:"dkim"`
	Spf         string                       `xml:"spf" json:"spf"`
	Reason      []SyslogPolicyOverrideReason `xml:"reason" json:"reason"`
}

type SyslogResultSPF struct {
	Domain string `xml:"domain" json:"domain"`
	Scope  string `xml:"scope" json:"scope"`
	Result string `xml:"result" json:"result"`
}

type SyslogResultDKIM struct {
	Domain      string `xml:"domain" json:"domain"`
	Selector    string `xml:"selector" json:"selector"`
	Result      string `xml:"result" json:"result"`
	HumanResult string `xml:"human_result" json:"human_result"`
}

type SyslogPolicyOverrideReason struct {
	Type    string `xml:"type" json:"type"`
	Comment string `xml:"comment" json:"comment"`
}

func ConvertToSyslogJSON(filename string, report XMLReport, dns *dns.CachedDNSResolver, eventID, eventCategory string) ([][]byte, error) {
	reports, err := convertXMLToSyslog(filename, report, dns, eventID, eventCategory)
	if err != nil {
		return nil, err
	}

	var ret [][]byte
	for _, report := range reports {
		jsonString, err := json.Marshal(report)
		if err != nil {
			return nil, fmt.Errorf("could not marshal JSON: %w", err)
		}
		ret = append(ret, jsonString)
	}
	return ret, nil
}

func ConvertToSyslogXML(filename string, report XMLReport, dns *dns.CachedDNSResolver, eventID, eventCategory string) ([][]byte, error) {
	reports, err := convertXMLToSyslog(filename, report, dns, eventID, eventCategory)
	if err != nil {
		return nil, err
	}

	var ret [][]byte
	for _, report := range reports {
		xmlString, err := xml.Marshal(report)
		if err != nil {
			return nil, fmt.Errorf("could not marshal XML: %w", err)
		}
		ret = append(ret, xmlString)
	}
	return ret, nil
}

func convertXMLToSyslog(filename string, report XMLReport, dns *dns.CachedDNSResolver, eventID, eventCategory string) ([]SyslogEntry, error) {
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

		var reasons []SyslogPolicyOverrideReason
		for _, r := range record.Row.PolicyEvaluated.Reason {
			// too error prone:
			// nolint:gosimple
			tmp := SyslogPolicyOverrideReason{
				Type:    r.Type,
				Comment: r.Comment,
			}
			reasons = append(reasons, tmp)
		}

		syslog := SyslogEntry{
			Version:          report.Version,
			Domain:           reportingDomain,
			DateBegin:        report.ReportMetadata.DateRange.Begin,
			DateEnd:          report.ReportMetadata.DateRange.End,
			DateBeginParsed:  CustomTime(time.Unix(report.ReportMetadata.DateRange.Begin, 0)),
			DateEndParsed:    CustomTime(time.Unix(report.ReportMetadata.DateRange.End, 0)),
			ReportID:         report.ReportMetadata.ReportID,
			OrgName:          report.ReportMetadata.OrgName,
			Email:            report.ReportMetadata.Email,
			ExtraContactInfo: report.ReportMetadata.ExtraContactInfo,
			Errors:           report.ReportMetadata.Error,
			SourceIP:         record.Row.SourceIP,
			SourceDNS:        domains,
			SourceDNSString:  strings.Join(domains, ", "),
			Count:            record.Row.Count,
			EnvelopeTo:       record.Identifiers.EnvelopeTo,
			EnvelopeFrom:     record.Identifiers.EnvelopeFrom,
			HeaderFrom:       record.Identifiers.HeaderFrom,
			PolicyPublished: SyslogPolicyPublished{
				Domain: report.PolicyPublished.Domain,
				Adkim:  report.PolicyPublished.Adkim,
				Aspf:   report.PolicyPublished.Aspf,
				P:      report.PolicyPublished.P,
				Sp:     report.PolicyPublished.Sp,
				Pct:    report.PolicyPublished.Pct,
				Fo:     report.PolicyPublished.Fo,
			},
			PolicyEvaluated: SyslogPolicyEvaluated{
				Disposition: record.Row.PolicyEvaluated.Disposition,
				Dkim:        record.Row.PolicyEvaluated.Dkim,
				Spf:         record.Row.PolicyEvaluated.Spf,
				Reason:      reasons,
			},
			ResultSpf: SyslogResultSPF{
				Domain: record.AuthResults.Spf.Domain,
				Scope:  record.AuthResults.Spf.Scope,
				Result: record.AuthResults.Spf.Result,
			},
			ResultDkim: SyslogResultDKIM{
				Domain:      record.AuthResults.Dkim.Domain,
				Selector:    record.AuthResults.Dkim.Selector,
				Result:      record.AuthResults.Dkim.Result,
				HumanResult: record.AuthResults.Dkim.HumanResult,
			},
			EventID:       eventID,
			EventCategory: eventCategory,
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
