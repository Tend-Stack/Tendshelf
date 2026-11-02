// Package smtpfake is a small SMTP server for tests. It listens on 127.0.0.1 with a self-signed
// certificate made per run, speaks just enough SMTP (EHLO, STARTTLS, AUTH PLAIN, MAIL, RCPT, DATA, QUIT)
// and records what it accepted. It can refuse at chosen steps and drop the connection after the message.
package smtpfake

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Options chooses how the server behaves.
type Options struct {
	ImplicitTLS bool // encrypted from the first byte (port 465 style)
	NoStartTLS  bool // do not offer STARTTLS
	User, Pass  string
	// Fail maps a step (auth, mail, rcpt, data) to the reply code to give there.
	Fail map[string]int
	// FailTimes fails only the first N times a step in Fail is reached, then lets it pass; 0 fails always.
	FailTimes int
	// DropAfterData closes the connection after reading the whole message, without answering it.
	DropAfterData bool
}

// Received is one accepted message.
type Received struct {
	From, To, Data, AuthUser string
	TLS                      bool
}

// Server is a running fake.
type Server struct {
	opts Options
	ln   net.Listener
	cert tls.Certificate
	pool *x509.CertPool

	mu       sync.Mutex
	msgs     []Received
	injected int
	sessions int
}

// Start runs a server until the test ends.
func Start(t testing.TB, o Options) *Server {
	t.Helper()
	cert, pool := certificate(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{opts: o, ln: ln, cert: cert, pool: pool}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func certificate(t testing.TB) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "smtpfake"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// Addr is host:port.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Host is the IP the server listens on.
func (s *Server) Host() string { h, _, _ := net.SplitHostPort(s.Addr()); return h }

// Port is the listening port.
func (s *Server) Port() int {
	_, p, _ := net.SplitHostPort(s.Addr())
	n, _ := strconv.Atoi(p)
	return n
}

// Pool trusts the server's certificate.
func (s *Server) Pool() *x509.CertPool { return s.pool }

// Sessions is how many connections were accepted.
func (s *Server) Sessions() int { s.mu.Lock(); defer s.mu.Unlock(); return s.sessions }

// Messages returns what was accepted so far.
func (s *Server) Messages() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Received(nil), s.msgs...)
}

func (s *Server) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.sessions++
		s.mu.Unlock()
		go s.session(c)
	}
}

// fail reports the code to refuse step with, or 0 to let it pass.
func (s *Server) fail(step string) int {
	code, ok := s.opts.Fail[step]
	if !ok {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opts.FailTimes > 0 && s.injected >= s.opts.FailTimes {
		return 0
	}
	s.injected++
	return code
}

func (s *Server) session(raw net.Conn) {
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(20 * time.Second))
	var conn net.Conn = raw
	secure := false
	if s.opts.ImplicitTLS {
		conn = tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{s.cert}})
		secure = true
	}
	r := bufio.NewReader(conn)
	say := func(f string, a ...any) { fmt.Fprintf(conn, f+"\r\n", a...) }
	say("220 smtpfake ready")
	var from, to, authUser string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			say("250-smtpfake")
			if !secure && !s.opts.NoStartTLS && !s.opts.ImplicitTLS {
				say("250-STARTTLS")
			}
			if s.opts.User != "" && secure {
				say("250-AUTH PLAIN")
			}
			say("250 8BITMIME")
		case "STARTTLS":
			if s.opts.NoStartTLS || secure {
				say("502 not available")
				continue
			}
			say("220 go ahead")
			tc := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{s.cert}})
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, secure = tc, true
			r = bufio.NewReader(conn)
		case "AUTH":
			if !secure {
				say("530 start TLS first")
				continue
			}
			if code := s.fail("auth"); code != 0 {
				say("%d auth refused", code)
				continue
			}
			fields := strings.Fields(arg)
			if len(fields) < 2 || !strings.EqualFold(fields[0], "PLAIN") {
				say("504 only PLAIN")
				continue
			}
			dec, _ := base64.StdEncoding.DecodeString(fields[1])
			parts := strings.Split(string(dec), "\x00")
			if len(parts) != 3 || parts[1] != s.opts.User || parts[2] != s.opts.Pass {
				say("535 bad credentials")
				continue
			}
			authUser = parts[1]
			say("235 welcome")
		case "MAIL":
			if s.opts.User != "" && authUser == "" {
				say("530 sign in first")
				continue
			}
			if code := s.fail("mail"); code != 0 {
				say("%d sender refused", code)
				continue
			}
			from = between(arg)
			say("250 ok")
		case "RCPT":
			if code := s.fail("rcpt"); code != 0 {
				say("%d recipient %s refused", code, between(arg))
				continue
			}
			to = between(arg)
			say("250 ok")
		case "DATA":
			say("354 go on")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			if code := s.fail("data"); code != 0 {
				say("%d message refused", code)
				continue
			}
			s.mu.Lock()
			s.msgs = append(s.msgs, Received{From: from, To: to, Data: b.String(), AuthUser: authUser, TLS: secure})
			s.mu.Unlock()
			if s.opts.DropAfterData {
				return
			}
			say("250 queued")
		case "QUIT":
			say("221 bye")
			return
		case "RSET", "NOOP":
			say("250 ok")
		default:
			say("500 unknown")
		}
	}
}

// between returns the address inside <...> of "FROM:<a@b>".
func between(arg string) string {
	i, j := strings.IndexByte(arg, '<'), strings.LastIndexByte(arg, '>')
	if i < 0 || j < i {
		return ""
	}
	return arg[i+1 : j]
}
