package share

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// This file is the only place Tendshelf dials a mail server, and only the one the owner set up, only
// when an invite is queued for sending or the owner asks for a test. It never sends in the clear to a
// server that is not on this computer, and it always checks the server's certificate.

// Bounds of one send attempt.
const (
	mailDialTimeout    = 10 * time.Second
	mailSessionTimeout = 30 * time.Second
	mailReadCap        = 64 << 10 // bytes read from the server in one session
	mailMessageCap     = 64 << 10 // bytes of one message
)

// MailOptions changes how mail is sent; the zero value is production.
type MailOptions struct {
	RootCAs *x509.CertPool                                           // extra trust for tests; nil means the system's
	Dial    func(ctx context.Context, addr string) (net.Conn, error) // nil means a plain TCP dial
	Now     func() time.Time
	Logger  *slog.Logger
}

// Message is one email: a bare address, a subject and a plain-text body.
type Message struct {
	To, Subject, Body string
}

// Where a send went wrong.
const (
	stageDial  = "dial"
	stageTLS   = "tls"
	stageAuth  = "auth"
	stageFrom  = "from"
	stageRcpt  = "rcpt"
	stageData  = "data"
	stageClose = "close"
)

type sendError struct {
	Stage string
	Err   error
}

func (e *sendError) Error() string { return e.Stage + ": " + e.Err.Error() }
func (e *sendError) Unwrap() error { return e.Err }

var (
	errNoStartTLS = errors.New("the server does not offer STARTTLS")
	errInsecure   = errors.New("unencrypted mail is only allowed to this computer")
	errNotReady   = errors.New("email is not set up")
	errHeader     = errors.New("a header value has a line break or is not valid")
	errTooBig     = errors.New("message too large")
	errReadCap    = errors.New("the server sent too much")
)

// limitConn stops reading once the server has sent more than the cap.
type limitConn struct {
	net.Conn
	left int
}

func (c *limitConn) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errReadCap
	}
	if len(p) > c.left {
		p = p[:c.left]
	}
	n, err := c.Conn.Read(p)
	c.left -= n
	return n, err
}

func (m *Mail) now() time.Time {
	if m.opts.Now != nil {
		return m.opts.Now()
	}
	return time.Now()
}

func (m *Mail) log() *slog.Logger {
	if m.opts.Logger != nil {
		return m.opts.Logger
	}
	return slog.Default()
}

func (m *Mail) dial(ctx context.Context, addr string) (net.Conn, error) {
	if m.opts.Dial != nil {
		return m.opts.Dial(ctx, addr)
	}
	d := net.Dialer{Timeout: mailDialTimeout}
	return d.DialContext(ctx, "tcp", addr)
}

// send delivers msg through cfg. Every failure is a *sendError or one of the sentinel errors.
func (m *Mail) send(ctx context.Context, cfg MailConfig, msg Message) error {
	if !cfg.Ready() {
		return errNotReady
	}
	if cfg.TLS == TLSNone && !IsLoopback(cfg.Host) {
		return errInsecure
	}
	data, err := buildMessage(cfg.From(), msg, m.now(), db.NewID("m"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, mailSessionTimeout)
	defer cancel()
	dialCtx, dialCancel := context.WithTimeout(ctx, mailDialTimeout)
	defer dialCancel()
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	raw, err := m.dial(dialCtx, addr)
	if err != nil {
		return &sendError{stageDial, err}
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { raw.Close() })
	defer stop()
	_ = raw.SetDeadline(time.Now().Add(mailSessionTimeout))
	tlsConf := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12, RootCAs: m.opts.RootCAs}
	var conn net.Conn = &limitConn{Conn: raw, left: mailReadCap}
	if cfg.TLS == TLSImplicit {
		tc := tls.Client(conn, tlsConf)
		if err := tc.HandshakeContext(ctx); err != nil {
			return &sendError{stageTLS, err}
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return &sendError{stageDial, err}
	}
	defer c.Close()
	if cfg.TLS == TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return &sendError{stageTLS, errNoStartTLS}
		}
		if err := c.StartTLS(tlsConf); err != nil {
			return &sendError{stageTLS, err}
		}
	}
	if cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return &sendError{stageAuth, errors.New("the server does not offer sign-in")}
		}
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return &sendError{stageAuth, err}
		}
	}
	if err := c.Mail(cfg.FromAddress); err != nil {
		return &sendError{stageFrom, err}
	}
	if err := c.Rcpt(msg.To); err != nil {
		return &sendError{stageRcpt, err}
	}
	w, err := c.Data()
	if err != nil {
		return &sendError{stageData, err}
	}
	if _, err := w.Write(data); err != nil {
		return &sendError{stageData, err}
	}
	if err := w.Close(); err != nil {
		return &sendError{stageClose, err}
	}
	_ = c.Quit()
	return nil
}

func hasLineBreak(s string) bool { return strings.ContainsAny(s, "\r\n\x00") }

// encodeSubject writes the subject as one header value: plain when it is simple ASCII, else Q-encoded
// words of at most 30 characters each, folded onto continuation lines.
func encodeSubject(s string) string {
	plain := true
	for i := 0; i < len(s); i++ {
		if s[i] < ' ' || s[i] >= utf8.RuneSelf {
			plain = false
		}
	}
	if plain && len(s) <= 900 {
		return s
	}
	var parts []string
	rs := []rune(s)
	for len(rs) > 0 {
		n := min(30, len(rs))
		parts = append(parts, mime.QEncoding.Encode("utf-8", string(rs[:n])))
		rs = rs[n:]
	}
	return strings.Join(parts, "\r\n ")
}

// buildMessage writes the whole message with CRLF line ends. Every header value comes from a checked
// part: a line break in any of them is errHeader.
func buildMessage(from mail.Address, msg Message, now time.Time, id string) ([]byte, error) {
	if hasLineBreak(from.Name) || hasLineBreak(from.Address) || hasLineBreak(msg.To) || hasLineBreak(msg.Subject) {
		return nil, errHeader
	}
	if p, err := mail.ParseAddress(msg.To); err != nil || p.Address != msg.To || !validSender(msg.To) {
		return nil, errHeader
	}
	at := strings.LastIndexByte(from.Address, '@')
	if at < 1 {
		return nil, errHeader
	}
	var b bytes.Buffer
	b.WriteString("From: " + from.String() + "\r\n")
	b.WriteString("To: " + msg.To + "\r\n")
	b.WriteString("Subject: " + encodeSubject(msg.Subject) + "\r\n")
	b.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: <" + id + from.Address[at:] + ">\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	body := strings.ReplaceAll(strings.ReplaceAll(msg.Body, "\r\n", "\n"), "\r", "\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := io.WriteString(qp, strings.ReplaceAll(body, "\n", "\r\n")); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	if b.Len() > mailMessageCap {
		return nil, errTooBig
	}
	return b.Bytes(), nil
}

// The fixed sentence for each failure code. None carries an address or a server reply.
var mailCodeText = map[string]string{
	"mail_auth":        "The email server did not accept the username or password.",
	"mail_sender":      "The email server will not send from that address.",
	"mail_recipient":   "The email server refused the recipient's address.",
	"mail_rejected":    "The email server refused the message.",
	"mail_no_tls":      "The email server does not offer a secure connection.",
	"mail_tls":         "Tendshelf could not check the email server's certificate.",
	"mail_unconfirmed": "The email server did not confirm the message arrived, so it was not sent again.",
	"mail_busy":        "The email server is busy.",
	"mail_unreachable": "Tendshelf could not reach the email server.",
	"mail_header":      "The message had a line break where it must not.",
	"mail_not_set_up":  "Email is not set up.",
	"mail_link_lost":   "The invite link is no longer in memory.",
}

func mailError(code string, permanent bool) *jobs.Error {
	return &jobs.Error{Code: code, Text: mailCodeText[code], Permanent: permanent}
}

var addressInReply = regexp.MustCompile(`[^\s<>"',;]+@[^\s<>"',;]+`)

// classify turns a send failure into the job error, and logs where it happened with the server's code
// and reply, addresses removed.
func (m *Mail) classify(err error) *jobs.Error {
	var se *sendError
	stage := ""
	if errors.As(err, &se) {
		stage = se.Stage
	}
	var te *textproto.Error
	isReply := errors.As(err, &te)
	code, reply := 0, ""
	if isReply {
		code, reply = te.Code, addressInReply.ReplaceAllString(te.Msg, "<address>")
	}
	m.log().Warn("email: send failed", "stage", stage, "smtp_code", code, "reply", reply)
	var ce x509.CertificateInvalidError
	var ue x509.UnknownAuthorityError
	var he x509.HostnameError
	var rh tls.RecordHeaderError
	switch {
	case errors.Is(err, errNotReady):
		return mailError("mail_not_set_up", true)
	case errors.Is(err, errHeader):
		return mailError("mail_header", true)
	case errors.Is(err, errInsecure), errors.Is(err, errNoStartTLS):
		return mailError("mail_no_tls", true)
	case errors.Is(err, errTooBig):
		return mailError("mail_rejected", true)
	case errors.As(err, &ce), errors.As(err, &ue), errors.As(err, &he), errors.As(err, &rh):
		return mailError("mail_tls", true)
	case stage == stageTLS && !isReply:
		return mailError("mail_tls", true)
	case stage == stageClose && !isReply:
		return mailError("mail_unconfirmed", true)
	case isReply && code >= 400 && code < 500:
		return mailError("mail_busy", false)
	case isReply && stage == stageAuth:
		return mailError("mail_auth", true)
	case isReply && stage == stageFrom:
		return mailError("mail_sender", true)
	case isReply && stage == stageRcpt:
		return mailError("mail_recipient", true)
	case isReply && stage == stageTLS:
		return mailError("mail_no_tls", true)
	case isReply:
		return mailError("mail_rejected", true)
	case stage == stageAuth:
		return mailError("mail_auth", true)
	}
	return mailError("mail_unreachable", false)
}

// mailOutbox holds the data a queued send needs but must not keep in the database: the invite's link
// (raw tokens are never stored) and the test address. It is memory only, so a restart loses it.
type mailOutbox struct {
	mu      sync.Mutex
	entries map[string]outboxEntry
	cap     int
	ttl     time.Duration
}

type outboxEntry struct {
	URL, To string
	At      time.Time
}

func newOutbox() *mailOutbox {
	return &mailOutbox{entries: map[string]outboxEntry{}, cap: 256, ttl: 2 * time.Hour}
}

func (o *mailOutbox) put(ref string, e outboxEntry) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for k, old := range o.entries {
		if e.At.Sub(old.At) > o.ttl {
			delete(o.entries, k)
		}
	}
	for len(o.entries) >= o.cap {
		oldest, first := "", true
		var at time.Time
		for k, v := range o.entries {
			if first || v.At.Before(at) {
				oldest, at, first = k, v.At, false
			}
		}
		delete(o.entries, oldest)
	}
	o.entries[ref] = e
}

func (o *mailOutbox) get(ref string, now time.Time) (outboxEntry, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	e, ok := o.entries[ref]
	if ok && now.Sub(e.At) > o.ttl {
		delete(o.entries, ref)
		return outboxEntry{}, false
	}
	return e, ok
}

func (o *mailOutbox) drop(ref string) {
	o.mu.Lock()
	delete(o.entries, ref)
	o.mu.Unlock()
}
