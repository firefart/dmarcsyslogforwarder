package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/syslog"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/firefart/dmarcsyslogforwarder/internal/config"
	"github.com/firefart/dmarcsyslogforwarder/internal/dmarc"
	"github.com/firefart/dmarcsyslogforwarder/internal/dns"
	"github.com/firefart/dmarcsyslogforwarder/internal/helper"
	"github.com/firefart/dmarcsyslogforwarder/internal/imap"

	goimap "github.com/emersion/go-imap"
	"github.com/emersion/go-message/mail"

	// needed to handle other charsets too
	_ "github.com/emersion/go-message/charset"

	"github.com/sirupsen/logrus"
)

type app struct {
	sysLog  *syslog.Writer
	dns     *dns.CachedDNSResolver
	config  *config.Configuration
	devMode bool
}

var (
	log = logrus.New()
)

func main() {
	debug := flag.Bool("debug", false, "Print debug output")
	devMode := flag.Bool("devmode", false, "enable dev mode (no syslog, no message delete and goroutine printing)")
	configFile := flag.String("config", "", "Config File to use")
	flag.Parse()

	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	if *debug {
		log.SetLevel(logrus.DebugLevel)
	}

	if *configFile == "" {
		log.Error("please supply a config file")
		return
	}

	// set some defaults
	defaults := config.Configuration{
		Format: "xml",
		FetchInterval: config.Duration{
			Duration: 1 * time.Hour,
		},
		BatchSize:      30,
		SyslogProtocol: "tcp",
		SyslogTag:      "dmarc",
		DnsConnectTimeout: config.Duration{
			Duration: 1 * time.Second,
		},
		DnsTimeout: config.Duration{
			Duration: 10 * time.Second,
		},
		DnsCacheTimeout: config.Duration{
			Duration: 1 * time.Hour,
		},
		EventID:       "",
		EventCategory: "",
	}

	settings, err := config.GetConfig(defaults, *configFile)
	if err != nil {
		log.Errorf("could not read %s: %v", *configFile, err)
		return
	}

	// trap Ctrl+C and call cancel on the context
	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	defer func() {
		signal.Stop(c)
		cancel()
	}()

	go func() {
		<-c
		log.Info("CTRL+C received")
		cancel()
	}()

	if err := run(ctx, settings, *devMode); err != nil {
		log.Error(err)
		return
	}
}

func run(ctx context.Context, settings *config.Configuration, devMode bool) error {
	var sysLog *syslog.Writer
	if !devMode {
		var err error
		sysLog, err = syslog.Dial(settings.SyslogProtocol, settings.SyslogServer,
			syslog.LOG_WARNING|syslog.LOG_DAEMON, settings.SyslogTag)
		if err != nil {
			return err
		}
		defer sysLog.Close()
	}

	dnsResolver := dns.NewCachedDNSResolver(ctx, settings.DnsServer, settings.DnsConnectTimeout.Duration, settings.DnsTimeout.Duration, settings.DnsCacheTimeout.Duration, log)

	app := app{
		sysLog:  sysLog,
		dns:     dnsResolver,
		config:  settings,
		devMode: devMode,
	}

	// print number of goroutines in devmode
	if devMode {
		go func() {
			goRoutineTicker := time.NewTicker(3 * time.Second)
			for {
				select {
				case <-goRoutineTicker.C:
					log.Debugf("number of goroutines: %d", runtime.NumGoroutine())
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// used to start the ticker immediately
	// otherwise it first runs after the first
	// period
	log.Info("starting first run")
	if err := app.imapLoop(ctx); err != nil {
		log.Errorf("Received error: %v", err)
	}
	log.Info("first run finished")

	ticker := time.NewTicker(settings.FetchInterval.Duration)

	for {
		select {
		case <-ctx.Done():
			log.Info("context done")
			ticker.Stop()
			return nil
		case <-ticker.C:
			log.Info("starting new run")
			if err := app.imapLoop(ctx); err != nil {
				// only log the error here so we keep the loop running
				log.Errorf("Received error: %v", err)
			}
			log.Info("run finished")
		}
	}
}

// run in batch sizes as some IMAP servers have pretty
// short timeouts and the imap library does not handle
// reconnects
func (a *app) imapLoop(ctx context.Context) error {
	var err error
	hasMore := true
	for hasMore {
		log.Debugf("starting new imap loop with batch size of %d", a.config.BatchSize)
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			hasMore, err = a.fetchIMAP(ctx)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (a *app) fetchIMAP(ctx context.Context) (bool, error) {
	c, err := imap.Connect(a.config.ImapConfig, log)
	if err != nil {
		return false, fmt.Errorf("could not connect to %s: %w", a.config.ImapConfig.Host, err)
	}

	log.Debug("connected to imap server")

	// also log IMAP messages in debug mode
	c.SetDebug(log.WriterLevel(logrus.DebugLevel))

	if err := c.Login(a.config.ImapConfig.User, a.config.ImapConfig.Pass); err != nil {
		return false, fmt.Errorf("could not login: %w", err)
	}

	log.Debug("successful login")

	defer func() {
		if err := c.Logout(); err != nil {
			log.Errorf("Error on logout: %v", err)
			return
		}
	}()

	hasFolder, err := imap.HasImapFolder(c, a.config.ImapConfig.Folder)
	if err != nil {
		return false, fmt.Errorf("could not check if folder %s exists: %w", a.config.ImapConfig.Folder, err)
	}

	if !hasFolder {
		return false, fmt.Errorf("imap folder %s not found in account", a.config.ImapConfig.Folder)
	}

	mbox, err := c.Select(a.config.ImapConfig.Folder, false)
	if err != nil {
		return false, fmt.Errorf("could not select folder %s: %w", a.config.ImapConfig.Folder, err)
	}

	log.Infof("Opened %s with %d messages (%d unread)", mbox.Name, mbox.Messages, mbox.Unseen)

	criteria := goimap.NewSearchCriteria()
	criteria.WithoutFlags = []string{goimap.DeletedFlag}
	ids, err := c.Search(criteria)
	if err != nil {
		return false, fmt.Errorf("could not search for mails: %w", err)
	}

	log.Debugf("found %d mails without the DELETED flag", len(ids))

	if len(ids) == 0 {
		// no mails to process
		return false, nil
	}
	seqset := new(goimap.SeqSet)

	hasMore := true
	if a.config.BatchSize >= len(ids) {
		// we got fewer ids than the batchsize, add them all
		seqset.AddNum(ids...)
		hasMore = false
	} else {
		for x, id := range ids {
			if x > a.config.BatchSize-1 {
				hasMore = true
				break
			}
			seqset.AddNum(id)
		}
	}

	log.Debugf("Fetching the following messages: %v", seqset.String())

	messages := make(chan *goimap.Message)
	done := make(chan error)

	// Get the whole message body
	section := &goimap.BodySectionName{}
	items := []goimap.FetchItem{
		section.FetchItem(),
		goimap.FetchBodyStructure,
		goimap.FetchEnvelope,
		goimap.FetchFlags,
		goimap.FetchInternalDate,
		goimap.FetchUid,
	}
	go func() {
		done <- c.Fetch(seqset, items, messages)
	}()

	msgCounter := 0
	toDelete := make(map[uint32]string)
	for msg := range messages {
		log.Infof("Processing email %s (UID %d)", msg.Envelope.Subject, msg.Uid)
		valid, err := a.processMessage(ctx, msg)
		if err != nil {
			log.Errorf("could not process message %d: %v", msg.Uid, err)
			// no continue here so we can check for a valid message
		}
		if valid {
			log.Debugf("adding message %d to delete set", msg.Uid)
		} else {
			log.Infof("Message %s does not seem to be a valid dmarc report. Marking it for deletion.", msg.Envelope.Subject)
		}
		// always delete a processed message to clean up junk behind
		toDelete[msg.Uid] = msg.Envelope.Subject
		msgCounter += 1
	}

	log.Debug("waiting for fetch to finish")

	if err := <-done; err != nil {
		return false, fmt.Errorf("error on fetch: %w", err)
	}

	if !a.devMode {
		for uid, subject := range toDelete {
			log.Infof("Marking message %s (UID: %d) as deleted", subject, uid)
			if err := imap.MarkMessageAsDeleted(c, uid); err != nil {
				log.Errorf("could not set delete flag on message %d: %v", uid, err)
				continue
			}
		}

		log.Info("Running expunge command (delete all marked messages)")
		if err := c.Expunge(nil); err != nil {
			return false, fmt.Errorf("could not expunge: %w", err)
		}
	}

	log.Infof("Processed %d emails", msgCounter)

	return hasMore, nil
}

func (a *app) processMessage(ctx context.Context, msg *goimap.Message) (bool, error) {
	// indicates if the email is a valid dmarc report
	validDmarcReport := false
	r := msg.GetBody(&goimap.BodySectionName{})
	if r == nil {
		return false, fmt.Errorf("server didn't return message body")
	}
	log.Debugf("body length %d", r.Len())
	m, err := mail.CreateReader(r)
	if err != nil {
		return false, fmt.Errorf("could not create reader: %w", err)
	}
	defer m.Close()
	log.Debug("reader created")

outer:
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
			log.Debug("before nextpart")
			p, err := m.NextPart()
			if err == io.EOF {
				log.Debug("EOF")
				break outer
			} else if err != nil {
				return false, fmt.Errorf("could not get next part: %w", err)
			}

			log.Debug("processing next part")

			switch h := p.Header.(type) {
			case *mail.InlineHeader:
				log.Debug("inline header")
				// This is the message's text (can be plain-text or HTML)
				b, err := io.ReadAll(p.Body)
				if err != nil {
					return false, fmt.Errorf("could not read inlineheader body: %w", err)
				}

				// sometimes the attachment is inlined to we check the magic bytes
				isArchive := helper.IsSupportedArchive(b)
				if isArchive {
					log.Info("found inline attachment")
					// try to get attachment filename from headers
					inlineHeader := p.Header.(*mail.InlineHeader)
					contentDisp, contentDispParams, err := inlineHeader.ContentDisposition()
					if err != nil {
						return false, fmt.Errorf("could not get contentdisposition: %w", err)
					}

					if contentDisp != "inline" {
						return false, fmt.Errorf("content disposition is not inline")
					}

					filename, ok := contentDispParams["filename"]
					if !ok {
						return false, fmt.Errorf("could not determine filename")
					}

					if err := a.sendAttachment(ctx, filename, b); err != nil {
						return false, err
					}
					// we parsed and sent the attachment so it's valid
					validDmarcReport = true
				} else {
					log.Debugf("%s", string(b))
				}
			case *mail.AttachmentHeader:
				log.Debug("attachment header")
				mailHeader := m.Header
				log.Debugf("Date: %s", mailHeader.Get("Date"))
				log.Debugf("From: %s", mailHeader.Get("From"))
				log.Debugf("To: %s", mailHeader.Get("To"))
				log.Debugf("Subject: %s", mailHeader.Get("Subject"))
				// This is an attachment
				filename, err := h.Filename()
				if err != nil {
					return false, fmt.Errorf("could not get attachment filename: %w", err)
				}

				b, err := io.ReadAll(p.Body)
				if err != nil {
					return false, fmt.Errorf("could not read attachment: %w", err)
				}

				if err := a.sendAttachment(ctx, filename, b); err != nil {
					return false, err
				}
				// we parsed and sent the attachment so it's valid
				validDmarcReport = true
			default:
				log.Infof("No header type implemented: %v", p.Header)
			}
		}
	}
	return validDmarcReport, nil
}

func (a *app) sendAttachment(ctx context.Context, filename string, body []byte) error {
	log.Infof("Got attachment: %s", filename)
	xmlFilename, xmlReport, err := dmarc.ReadFile(ctx, filename, body)
	if err != nil {
		return fmt.Errorf("could not read file %s: %w", filename, err)
	}

	var r [][]byte
	switch a.config.Format {
	case "xml":
		r, err = dmarc.ConvertToSyslogXML(xmlFilename, *xmlReport, a.dns, a.config.EventID, a.config.EventCategory)
		if err != nil {
			return fmt.Errorf("could not convert XML: %w", err)
		}
	case "json":
		r, err = dmarc.ConvertToSyslogJSON(xmlFilename, *xmlReport, a.dns, a.config.EventID, a.config.EventCategory)
		if err != nil {
			return fmt.Errorf("could not convert JSON: %w", err)
		}
	default:
		return fmt.Errorf("invalid format %s", a.config.Format)
	}

	for _, report := range r {
		log.Debugf("Converted entry: %s", string(report))

		// hint: we can't check the number returned here because
		// it's just the len of the input, so pretty useless
		if !a.devMode {
			_, err = a.sysLog.Write(report)
			if err != nil {
				return fmt.Errorf("could not send syslog entry: %w", err)
			}
			log.Debug("wrote message to syslog")
		}
	}

	return nil
}
