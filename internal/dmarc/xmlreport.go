package dmarc

// XMLReport represents the top element of a DMARC report
// https://tools.ietf.org/html/rfc7489#appendix-C
// also see report.xsd in this repository
type XMLReport struct {
	Version        string `xml:"version"`
	ReportMetadata struct {
		OrgName          string `xml:"org_name"`
		Email            string `xml:"email"`
		ExtraContactInfo string `xml:"extra_contact_info"`
		ReportID         string `xml:"report_id"`
		DateRange        struct {
			Begin int64 `xml:"begin"`
			End   int64 `xml:"end"`
		} `xml:"date_range"`
		Error []string `xml:"error"`
	} `xml:"report_metadata" `
	PolicyPublished struct {
		Domain string `xml:"domain"`
		Adkim  string `xml:"adkim"`
		Aspf   string `xml:"aspf"`
		P      string `xml:"p"`
		Sp     string `xml:"sp"`
		Pct    string `xml:"pct"`
		Fo     string `xml:"fo" `
	} `xml:"policy_published"`
	Records []Record `xml:"record"`
}

// Record represents the record element of a DMARC report
type Record struct {
	Row struct {
		SourceIP        string `xml:"source_ip"`
		Count           int    `xml:"count"`
		PolicyEvaluated struct {
			Disposition string                 `xml:"disposition"`
			Dkim        string                 `xml:"dkim"`
			Spf         string                 `xml:"spf"`
			Reason      []PolicyOverrideReason `xml:"reason"`
		} `xml:"policy_evaluated"`
	} `xml:"row"`
	Identifiers struct {
		EnvelopeTo   string `xml:"envelope_to"`
		HeaderFrom   string `xml:"header_from"`
		EnvelopeFrom string `xml:"envelope_from"`
	} `xml:"identifiers"`
	AuthResults struct {
		Spf struct {
			Domain string `xml:"domain"`
			Scope  string `xml:"scope"`
			Result string `xml:"result"`
		} `xml:"spf"`
		Dkim struct {
			Domain      string `xml:"domain"`
			Selector    string `xml:"selector"`
			Result      string `xml:"result"`
			HumanResult string `xml:"human_result"`
		} `xml:"dkim"`
	} `xml:"auth_results"`
}

// PolicyOverrideReason represents the reason element of a DMARC report
type PolicyOverrideReason struct {
	Type    string `xml:"type"`
	Comment string `xml:"comment"`
}
