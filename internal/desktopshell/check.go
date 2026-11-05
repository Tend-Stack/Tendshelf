package desktopshell

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The reasons a server is refused. Each message is the words the person sees.
var (
	ErrAddress      = errors.New("Enter the address you use in your browser, like https://books.example.com.")
	ErrUnreachable  = errors.New("Couldn't reach that server. Check the address and that it's running.")
	ErrCertificate  = errors.New("That server's certificate isn't trusted, so Tendshelf won't connect.")
	ErrNotTendshelf = errors.New("That address doesn't look like a Tendshelf server.")
	ErrOldServer    = errors.New("That server needs updating before this app can use it.")
)

// Words returns the sentence to show for err: the message of one of the errors above, else a
// general line (details belong in the log, not on screen).
func Words(err error) string {
	// The desktop module's own failures (a library that will not open) carry their words.
	var spoken interface{ Words() string }
	if errors.As(err, &spoken) {
		return spoken.Words()
	}
	for _, known := range []error{ErrAddress, ErrUnreachable, ErrCertificate, ErrNotTendshelf, ErrOldServer} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return "Something went wrong. Please try again."
}

// NormalizeServer turns what a person typed into an origin ("https://host[:port]"). No scheme
// means https. Only http and https pass; user info, a query, a fragment and any path but "/" are
// refused. The host is lower-cased and a trailing slash dropped.
func NormalizeServer(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, " \t\r\n\\") {
		return "", ErrAddress
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrAddress
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrAddress
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", ErrAddress
	}
	if u.Path != "" && u.Path != "/" {
		return "", ErrAddress
	}
	if u.Opaque != "" || u.Hostname() == "" {
		return "", ErrAddress
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") { // an IPv6 literal
		host = "[" + host + "]"
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", ErrAddress
		}
		host += ":" + p
	}
	return u.Scheme + "://" + host, nil
}

// IsPrivateHost reports whether origin names a machine on this computer or a home or office
// network: loopback, private and link-local addresses, a single-label name, or .local, .lan,
// .home.arpa and .internal. A plain http address anywhere else would send a password in the clear.
func IsPrivateHost(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	if !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".local", ".lan", ".home.arpa", ".internal"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// CheckServer asks origin's /healthz whether it is a Tendshelf server this app can use. It is the
// shell's only own request: no credentials, no cookie jar, redirects not followed, 5 seconds, and at
// most 4 KiB read (decision 0020; AGENTS.md "Security and privacy").
func CheckServer(ctx context.Context, origin string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/healthz", nil)
	if err != nil {
		return ErrAddress
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return classify(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ErrNotTendshelf // includes every redirect
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return ErrUnreachable
	}
	var health map[string]any
	if json.Unmarshal(body, &health) != nil {
		return ErrNotTendshelf
	}
	_, hasVersion := health["version"]
	if health["status"] != "ok" || !hasVersion {
		return ErrNotTendshelf
	}
	if app, ok := health["app"]; ok && app != "tendshelf" {
		return ErrNotTendshelf
	}
	if _, ok := health["shell"]; !ok {
		return ErrOldServer
	}
	return nil
}

// classify sorts a transport error into a certificate problem or an unreachable server.
func classify(err error) error {
	var verify *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &verify) || errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname) {
		return ErrCertificate
	}
	return ErrUnreachable
}
