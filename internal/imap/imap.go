package imap

import (
	"crypto/tls"
	"errors"
	"net"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/firefart/dmarcsyslogforwarder/internal/config"
)

// ErrNoTLS is returned if the server does not support STARTTLS and plaintext
// connections are not explicitly allowed
var ErrNoTLS = errors.New("server does not support STARTTLS, refusing to send credentials in plaintext (set imap.allowPlaintext to override)")

func Connect(conf config.IMAPConfig, logger imap.Logger) (*client.Client, error) {
	tlsConfig := tls.Config{} // nolint: gosec
	if conf.IgnoreCert {
		tlsConfig.InsecureSkipVerify = true // nolint:gosec
	}
	dialer := &net.Dialer{
		Timeout: conf.Timeout.Duration,
	}

	var c *client.Client
	var err error
	if conf.SSL {
		c, err = client.DialWithDialerTLS(dialer, conf.Host, &tlsConfig)
	} else {
		c, err = client.DialWithDialer(dialer, conf.Host)
	}
	if err != nil {
		return nil, err
	}
	c.ErrorLog = logger
	c.Timeout = conf.Timeout.Duration

	if conf.SSL {
		return c, nil
	}

	support, err := c.SupportStartTLS()
	if err != nil {
		_ = c.Logout()
		return nil, err
	}
	if !support {
		if conf.AllowPlaintext {
			return c, nil
		}
		_ = c.Logout()
		return nil, ErrNoTLS
	}
	if err := c.StartTLS(&tlsConfig); err != nil {
		_ = c.Logout()
		return nil, err
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
	// always drain the channel so the List command can finish
	for m := range mailboxes {
		if m.Name == folderName {
			hasFolder = true
		}
	}

	if err := <-done; err != nil {
		return false, err
	}

	return hasFolder, nil
}

// MarkMessagesAsDeleted sets the deleted flag on all messages with the given UIDs
func MarkMessagesAsDeleted(c *client.Client, msgUIDs []uint32) error {
	if len(msgUIDs) == 0 {
		return nil
	}
	seq := new(imap.SeqSet)
	seq.AddNum(msgUIDs...)
	item := imap.FormatFlagsOp(imap.AddFlags, true)
	flags := []any{imap.DeletedFlag}
	return c.UidStore(seq, item, flags, nil)
}
