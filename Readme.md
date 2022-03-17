# DMARCSYSLOGFORWARDER

This program is used to read in dmarc reports from a dedicated IMAP mailbox and converts them to single entries and sends them in XML or JSON format via syslog to a remote server. This can be used to feed dmarc reports into your favourite SIEM.

As each dmarc report can contain multiple entries the report is split into single reports. The source ip from the report is also resolved via DNS.

This program does not work on windows as the golang syslog library is not compatible.

## Syslog JSON Format

```json
{
  "event_id": "",
  "event_category": "",
  "version": "1.0",
  "domain": "google.com",
  "date_begin": 1636416000,
  "date_end": 1636502399,
  "report_id": "",
  "org_name": "",
  "email": "noreply-dmarc-support@google.com",
  "extra_contact_info": "https://support.google.com/a/answer/2466580",
  "errors": null,
  "source_ip": "",
  "source_dns": [
    "domain1",
    "domain2",
    "domain3"
  ],
  "source_dns_string": "domain1, domain2, domain3",
  "count": 1,
  "envelope_to": "",
  "header_from": "",
  "envelope_from": "",
  "policy_published": {
    "domain": "",
    "adkim": "",
    "aspf": "",
    "p": "",
    "sp": "",
    "pct": "",
    "fo": ""
  },
  "policy_evaluated": {
    "disposition": "",
    "dkim": "",
    "spf": "",
    "reason": null
  },
  "result_spf": {
    "domain": "",
    "scope": "",
    "result": ""
  },
  "result_dkim": {
    "domain": "",
    "selector": "",
    "result": "",
    "human_result": ""
  }
}
```

## Syslog XML Format

```xml
<syslog_entry>
  <!-- SIEM specif fields start (ommitted when empty) -->
  <event_id>FROM_CONFIG</event_id>
  <event_category>FROM_CONFIG</event_category>
  <!-- SIEM specif fields end -->
  <version></version>
  <domain>google.com</domain>
  <date_begin>1636416000</date_begin>
  <date_end>1636502399</date_end>
  <report_id></report_id>
  <org_name>google.com</org_name>
  <email>noreply-dmarc-support@google.com</email>
  <extra_contact_info>https://support.google.com/a/answer/2466580</extra_contact_info>
  <errors>
    <error></error>
    <error></error>
    <error></error>
  </errors>
  <source_ip></source_ip>
  <source_dns>
    <dns>domain1</dns>
    <dns>domain2</dns>
    <dns>domain3</dns>
  </source_dns>
  <source_dns_string>domain1, domain2, domain3</source_dns_string>
  <count></count>
  <envelope_to></envelope_to>
  <header_from></header_from>
  <envelope_from></envelope_from>
  <policy_published>
    <domain></domain>
    <adkim></adkim>
    <aspf></aspf>
    <p></p>
    <sp></sp>
    <pct></pct>
    <fo></fo>
  </policy_published>
  <policy_evaluated>
    <disposition></disposition>
    <dkim></dkim>
    <spf></spf>
    <reason>
      <type></type>
      <comment></comment>
    </reason>
    <reason>
      <type></type>
      <comment></comment>
    </reason>
  </policy_evaluated>
  <result_spf>
    <domain></domain>
    <scope></scope>
    <result></result>
  </result_spf>
  <result_dkim>
    <domain></domain>
    <selector></selector>
    <result></result>
    <human_result></human_result>
  </result_dkim>
</syslog_entry>
```

## Config File

See the `config.example.json` for an example.

| Fieldname | Description |
|---|---|
| format | can either be xml or json |
| fetchInterval | How often should the job fetch emails from the IMAP server and process them |
| syslogServer | The syslog server in the format ip:port |
| syslogProtocol | The syslog protocol. can be tcp, udp or "". On empty string the local unix socket is used |
| syslogTag | The syslog tag to add to all messages |
| dnsServer | a custom DNS server to use for queries. Uses the system default if left empty |
| dnsConnectTimeout | timeout when connecting to the DNS server |
| dnsTimeout | timeout when waiting on DNS answers |
| dnsCacheTimeout | how long should DNS answers be cached |
| batchSize | how many emails to fetch per login/logout run. As the IMAP server can simply close connections on timeout and the library can not handle reconnects the mails are fetched in multuple runs |
| eventID | Value that will be serialized into "EventID". May be needed by your SIEM to match the logs against a log type. This field does not appear in the XML if it's left empty. |
| eventCategory| Value that will be serialized into "EventCategory". May be needed by your SIEM to match the logs against a log type. This field does not appear in the XML if it's left empty. |
| imap.host | IMAP server in the format ip:port |
| imap.ssl | use SSL/TLS when connecting to server |
| imap.user | IMAP username |
| imap.pass | IMAP password |
| imap.folder | the IMAP folder the reports are in |
| imap.ignoreCert | Ignore invalid TLS certificates when connecting to the IMAP server |

## Installation

```bash
adduser --system dmarc
mkdir /home/dmarc
chown -R dmarc:dmarc /home/dmarc
cd /home/dmarc
git clone https://github.com/FireFart/dmarcsyslogforwarder.git
cd dmarcsyslogforwarder
make
./install_service.sh
```
