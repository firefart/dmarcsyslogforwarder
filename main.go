package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"log/syslog"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/firefart/dmarcsyslogforwarder/internal/config"
	"github.com/firefart/dmarcsyslogforwarder/internal/dmarc"
	"github.com/firefart/dmarcsyslogforwarder/internal/dns"
	"github.com/firefart/dmarcsyslogforwarder/internal/helper"
	"github.com/firefart/dmarcsyslogforwarder/internal/imap"

	goimap "github.com/emersion/go-imap"
	"github.com/emersion/go-message/mail"
	"github.com/hashicorp/go-multierror"
	// needed to handle other charsets too
	_ "github.com/emersion/go-message/charset"
)

type app struct {
	sysLog    *syslog.Writer
	dns       *dns.CachedDNSResolver
	config    config.Configuration
	devMode   bool
	debugMode bool
	log       *slog.Logger
}

func main() {
	debugMode := flag.Bool("debug", false, "Print debug output")
	jsonOutput := flag.Bool("json", false, "output in json instead")
	devMode := flag.Bool("devmode", false, "enable dev mode (no syslog, no message delete and goroutine printing)")
	configFile := flag.String("config", "", "Config File to use")
	configCheckMode := flag.Bool("configcheck", false, "just check the config")
	version := flag.Bool("version", false, "show version")
	flag.Parse()

	if *version {
		buildInfo, ok := debug.ReadBuildInfo()
		if !ok {
			fmt.Println("Unable to determine version information")
			os.Exit(1)
		}
		fmt.Printf("%s", buildInfo)
		os.Exit(0)
	}

	logger := newLogger(*debugMode, *jsonOutput)

	var err error
	if *configCheckMode {
		err = configCheck(*configFile)
	} else {
		settings, err2 := config.GetConfig(*configFile)
		if err2 != nil {
			logger.Error("could not read config", slog.String("filename", *configFile), slog.String("err", err2.Error()))
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
			logger.Info("CTRL+C received")
			cancel()
		}()

		err = run(ctx, settings, logger, *devMode, *debugMode)
	}

	if err != nil {
		// check if we have a multierror
		var merr *multierror.Error
		if errors.As(err, &merr) {
			for _, e := range merr.Errors {
				logger.Error(e.Error())
			}
			os.Exit(1)
		}
		// a normal error
		logger.Error(err.Error())
		os.Exit(1)
	}

}

func configCheck(configFilename string) error {
	_, err := config.GetConfig(configFilename)
	return err
}

func run(ctx context.Context, settings config.Configuration, logger *slog.Logger, devMode bool, debugMode bool) error {
	var sysLog *syslog.Writer
	if !devMode {
		var err error
		sysLog, err = syslog.Dial(settings.SyslogProtocol, settings.SyslogServer,
			syslog.LOG_WARNING|syslog.LOG_DAEMON, settings.SyslogTag)
		if err != nil {
			return err
		}
		defer func(sysLog *syslog.Writer) {
			err := sysLog.Close()
			if err != nil {
				logger.Error("error closing syslog", slog.String("err", err.Error()))
			}
		}(sysLog)
	}

	dnsResolver := dns.NewCachedDNSResolver(ctx, settings.DNSServer, settings.DNSConnectTimeout.Duration, settings.DNSTimeout.Duration, settings.DNSCacheTimeout.Duration, logger)

	app := app{
		sysLog:    sysLog,
		dns:       dnsResolver,
		config:    settings,
		devMode:   devMode,
		log:       logger,
		debugMode: debugMode,
	}

	// print number of goroutines in devmode
	if devMode {
		go func() {
			goRoutineTicker := time.NewTicker(3 * time.Second)
			for {
				select {
				case <-goRoutineTicker.C:
					app.log.Debug("goroutines", slog.Int("count", runtime.NumGoroutine()))
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// used to start the ticker immediately
	// otherwise it first runs after the first
	// period
	app.log.Info("starting first run")
	if err := app.imapLoop(ctx); err != nil {
		app.log.Error("Received error", slog.String("err", err.Error()))
	}
	app.log.Info("first run finished")

	ticker := time.NewTicker(settings.FetchInterval.Duration)

	for {
		select {
		case <-ctx.Done():
			app.log.Info("context done")
			ticker.Stop()
			return nil
		case <-ticker.C:
			app.log.Info("starting new run")
			if err := app.imapLoop(ctx); err != nil {
				// only log the error here, so we keep the loop running
				app.log.Error("Received error", slog.String("err", err.Error()))
			}
			app.log.Info("run finished")
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
		a.log.Debug("starting new imap loop", slog.Int("batch-size", a.config.BatchSize))
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

type imapLogger struct {
	log *slog.Logger
}

func (l imapLogger) Printf(format string, v ...interface{}) {
	l.log.Info(fmt.Sprintf(format, v...))
}

func (l imapLogger) Println(v ...interface{}) {
	l.log.Info(fmt.Sprintln(v...))
}

func (a *app) fetchIMAP(ctx context.Context) (bool, error) {
	imapLog := imapLogger{log: a.log}
	c, err := imap.Connect(a.config.ImapConfig, imapLog)
	if err != nil {
		return false, fmt.Errorf("could not connect to %s: %w", a.config.ImapConfig.Host, err)
	}

	a.log.Debug("connected to imap server")

	// also log IMAP messages in debug mode
	if a.debugMode {
		c.SetDebug(os.Stdout)
	}

	if err := c.Login(a.config.ImapConfig.User, a.config.ImapConfig.Pass); err != nil {
		return false, fmt.Errorf("could not login: %w", err)
	}

	a.log.Debug("successful login")

	defer func() {
		if err := c.Logout(); err != nil {
			a.log.Error("Error on logout", slog.String("err", err.Error()))
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

	a.log.Info("Opened mailbox",
		slog.String("mailbox", mbox.Name),
		slog.Int("message-count", int(mbox.Messages)),
		slog.Int("unread-count", int(mbox.Unseen)),
	)

	criteria := goimap.NewSearchCriteria()
	criteria.WithoutFlags = []string{goimap.DeletedFlag}
	ids, err := c.Search(criteria)
	if err != nil {
		return false, fmt.Errorf("could not search for mails: %w", err)
	}

	a.log.Debug("found mails without the DELETED flag", slog.Int("count", len(ids)))

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

	a.log.Debug("Fetching messages", slog.String("messages", seqset.String()))

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
		a.log.Info("Processing email", slog.String("subject", msg.Envelope.Subject), slog.Int("uid", int(msg.Uid)))
		valid, err := a.processMessage(ctx, msg)
		if err != nil {
			a.log.Error("could not process message", slog.Int("uid", int(msg.Uid)), slog.String("err", err.Error()))
			// no continue here, so we can check for a valid message
		}
		if valid {
			a.log.Debug("adding message to delete set", slog.Int("uid", int(msg.Uid)))
		} else {
			a.log.Info("Message does not seem to be a valid dmarc report. Marking it for deletion.", slog.String("subject", msg.Envelope.Subject))
		}
		// always delete a processed message to clean up junk behind
		toDelete[msg.Uid] = msg.Envelope.Subject
		msgCounter++
	}

	a.log.Debug("waiting for fetch to finish")

	if err := <-done; err != nil {
		return false, fmt.Errorf("error on fetch: %w", err)
	}

	if !a.devMode {
		for uid, subject := range toDelete {
			a.log.Info("Marking message as deleted", slog.String("subject", subject), slog.Int("uid", int(uid)))
			if err := imap.MarkMessageAsDeleted(c, uid); err != nil {
				a.log.Error("could not set delete flag on message", slog.Int("uid", int(uid)), slog.String("err", err.Error()))
				continue
			}
		}

		a.log.Info("Running expunge command (delete all marked messages)")
		if err := c.Expunge(nil); err != nil {
			return false, fmt.Errorf("could not expunge: %w", err)
		}
	}

	a.log.Info("Processed emails", slog.Int("count", msgCounter))

	return hasMore, nil
}

func (a *app) processMessage(ctx context.Context, msg *goimap.Message) (bool, error) {
	// indicates if the email is a valid dmarc report
	validDmarcReport := false
	r := msg.GetBody(&goimap.BodySectionName{})
	if r == nil {
		return false, fmt.Errorf("server didn't return message body")
	}
	a.log.Debug("body length", slog.Int("len", r.Len()))
	m, err := mail.CreateReader(r)
	if err != nil {
		return false, fmt.Errorf("could not create reader: %w", err)
	}
	defer func(m *mail.Reader) {
		err := m.Close()
		if err != nil {
			a.log.Error("error on closing mail reader", slog.String("err", err.Error()))
		}
	}(m)
	a.log.Debug("reader created")

outer:
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
			a.log.Debug("before nextpart")
			p, err := m.NextPart()
			if err == io.EOF {
				a.log.Debug("EOF")
				break outer
			} else if err != nil {
				return false, fmt.Errorf("could not get next part: %w", err)
			}

			a.log.Debug("processing next part")

			switch h := p.Header.(type) {
			case *mail.InlineHeader:
				a.log.Debug("inline header")
				// This is the message's text (can be plain-text or HTML)
				b, err := io.ReadAll(p.Body)
				if err != nil {
					return false, fmt.Errorf("could not read inlineheader body: %w", err)
				}

				// sometimes the attachment is inlined to we check the magic bytes
				isArchive := helper.IsSupportedArchive(b)
				if isArchive {
					a.log.Info("found inline attachment")
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

					if err := a.sendAttachment(filename, b); err != nil {
						return false, err
					}
					// we parsed and sent the attachment so it's valid
					validDmarcReport = true
				} else {
					a.log.Debug("message", slog.String("content", string(b)))
				}
			case *mail.AttachmentHeader:
				mailHeader := m.Header
				a.log.Debug("attachment header",
					slog.String("date", mailHeader.Get("Date")),
					slog.String("from", mailHeader.Get("From")),
					slog.String("to", mailHeader.Get("To")),
					slog.String("subject", mailHeader.Get("Subject")),
				)
				// This is an attachment
				filename, err := h.Filename()
				if err != nil {
					return false, fmt.Errorf("could not get attachment filename: %w", err)
				}

				b, err := io.ReadAll(p.Body)
				if err != nil {
					return false, fmt.Errorf("could not read attachment: %w", err)
				}

				if err := a.sendAttachment(filename, b); err != nil {
					return false, err
				}
				// we parsed and sent the attachment so it's valid
				validDmarcReport = true
			default:
				a.log.Info("header type not implemented", slog.String("header", fmt.Sprintf("%v", p.Header)))
			}
		}
	}
	return validDmarcReport, nil
}

func (a *app) sendAttachment(filename string, body []byte) error {
	a.log.Info("Got attachment", slog.String("filename", filename))
	xmlFilename, xmlReport, err := dmarc.ReadFile(filename, body)
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
		a.log.Debug("Converted entry", slog.String("report", string(report)))

		// hint: we can't check the number returned here because
		// it's just the len of the input, so pretty useless
		if !a.devMode {
			_, err = a.sysLog.Write(report)
			if err != nil {
				return fmt.Errorf("could not send syslog entry: %w", err)
			}
			a.log.Debug("wrote message to syslog")
		}
	}

	return nil
}
