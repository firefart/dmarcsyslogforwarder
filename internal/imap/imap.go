package imap

import (
	"crypto/tls"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/firefart/dmarcsyslogforwarder/internal/config"
)

func Connect(conf config.IMAPConfig, logger imap.Logger) (*client.Client, error) {
	tlsConfig := tls.Config{} // nolint: gosec
	if conf.IgnoreCert {
		tlsConfig.InsecureSkipVerify = true // nolint:gosec
	}
	if conf.SSL {
		c, err := client.DialTLS(conf.Host, &tlsConfig)
		if err != nil {
			return nil, err
		}
		c.Timeout = conf.Timeout.Duration
		c.ErrorLog = logger
		return c, nil
	}
	c, err := client.Dial(conf.Host)
	if err != nil {
		return nil, err
	}
	c.ErrorLog = logger
	c.Timeout = conf.Timeout.Duration
	support, err := c.SupportStartTLS()
	if err != nil {
		return nil, err
	}
	if support {
		if err := c.StartTLS(&tlsConfig); err != nil {
			return nil, err
		}
	}

	return c, nil
}

func HasImapFolder(c *client.Client, folderName string) (bool, error) {
	mailboxes := make(chan *imap.MailboxInfo, 10)
	done := make(chan error, 1)
	go func() {
		done <- c.List("", "*", mailboxes)
	}()

	hasFolder := false
	for m := range mailboxes {
		if m.Name == folderName {
			hasFolder = true
			break
		}
	}

	if err := <-done; err != nil {
		return false, err
	}

	return hasFolder, nil
}

func MarkMessageAsDeleted(c *client.Client, msgUID uint32) error {
	seq := new(imap.SeqSet)
	seq.AddNum(msgUID)
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []interface{}{imap.DeletedFlag}
	if err := c.UidStore(seq, item, flags, nil); err != nil {
		return err
	}
	return nil
}
