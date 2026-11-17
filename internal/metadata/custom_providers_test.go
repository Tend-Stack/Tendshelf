package metadata_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

const secretHeader = "Bearer s3cr3t-header-value"

// siteServer answers the lookup-site format on loopback and counts what it was asked.
func siteServer(t *testing.T, body string, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func loopbackAddr(srv *httptest.Server) string {
	_, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return "http://127.0.0.1:" + p
}

func fieldCode(err error) (string, string) {
	var fe *metadata.ProviderFieldError
	if errors.As(err, &fe) {
		return fe.Field, fe.Code
	}
	return "", fmt.Sprint(err)
}

func TestAddCustomProviderValidates(t *testing.T) {
	e := newLookupEnv(t)
	if _, err := e.svc.AddCustomProvider(bg, metadata.CustomProviderInput{Name: "Taken", Address: "https://one.example"}, ""); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name         string
		in           metadata.CustomProviderInput
		field, code  string
		wantStoredAs string
	}{
		{"empty name", metadata.CustomProviderInput{Name: "  ", Address: "https://a.example"}, "name", "name", ""},
		{"long name", metadata.CustomProviderInput{Name: strings.Repeat("é", 41), Address: "https://a.example"}, "name", "name", ""},
		{"name taken in other case", metadata.CustomProviderInput{Name: "TAKEN", Address: "https://a.example"}, "name", "name_taken", ""},
		{"no scheme", metadata.CustomProviderInput{Name: "A", Address: "a.example"}, "address", "address", ""},
		{"ftp", metadata.CustomProviderInput{Name: "A", Address: "ftp://a.example"}, "address", "address", ""},
		{"no host", metadata.CustomProviderInput{Name: "A", Address: "https://"}, "address", "address", ""},
		{"user info", metadata.CustomProviderInput{Name: "A", Address: "https://u:p@a.example"}, "address", "address_parts", ""},
		{"query", metadata.CustomProviderInput{Name: "A", Address: "https://a.example/x?token=1"}, "address", "address_parts", ""},
		{"fragment", metadata.CustomProviderInput{Name: "A", Address: "https://a.example/x#y"}, "address", "address_parts", ""},
		{"port 25", metadata.CustomProviderInput{Name: "A", Address: "http://192.168.1.2:25"}, "address", "port", ""},
		{"link-local literal", metadata.CustomProviderInput{Name: "A", Address: "http://169.254.169.254"}, "address", "blocked", ""},
		{"public literal over http", metadata.CustomProviderInput{Name: "A", Address: "http://8.8.8.8"}, "address", "insecure", ""},
		{"header too long", metadata.CustomProviderInput{Name: "A", Address: "https://a.example", Auth: strings.Repeat("a", 1001)}, "auth", "auth", ""},
		{"header with line break", metadata.CustomProviderInput{Name: "A", Address: "https://a.example", Auth: "x\r\nHost: y"}, "auth", "auth", ""},
		{"header with non-ascii", metadata.CustomProviderInput{Name: "A", Address: "https://a.example", Auth: "tök"}, "auth", "auth", ""},
		{"ok, normalised", metadata.CustomProviderInput{Name: " Mine ", Address: " HTTPS://Books.Example.ORG:8443/abs/ ", Auth: secretHeader}, "", "", "https://books.example.org:8443/abs"},
		{"ok, private literal over http", metadata.CustomProviderInput{Name: "Home", Address: "http://192.168.1.20:3001"}, "", "", "http://192.168.1.20:3001"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, err := e.svc.AddCustomProvider(bg, tc.in, "")
			f, c := fieldCode(err)
			if f != tc.field || (tc.code != "" && c != tc.code) {
				t.Fatalf("err = %v (%s %s), want %s %s", err, f, c, tc.field, tc.code)
			}
			if tc.code != "" {
				return
			}
			var base string
			if err := e.st.Reader().QueryRow(`SELECT base_url FROM custom_providers WHERE id = ?`, id).Scan(&base); err != nil || base != tc.wantStoredAs {
				t.Errorf("stored %q, %v; want %q", base, err, tc.wantStoredAs)
			}
		})
	}
	// The list now holds Taken, Mine and Home; fill it to ten, then the eleventh is refused.
	for i := 0; i < 7; i++ {
		if _, err := e.svc.AddCustomProvider(bg, metadata.CustomProviderInput{Name: fmt.Sprintf("Site %d", i), Address: "https://x.example"}, ""); err != nil {
			t.Fatalf("site %d: %v", i, err)
		}
	}
	if _, err := e.svc.AddCustomProvider(bg, metadata.CustomProviderInput{Name: "Eleven", Address: "https://x.example"}, ""); fmt.Sprint(err) == "<nil>" {
		t.Error("an eleventh site was accepted")
	} else if _, c := fieldCode(err); c != "too_many" {
		t.Errorf("err = %v, want too_many", err)
	}
	list, err := e.svc.List(bg)
	if err != nil || len(list) != 10 {
		t.Fatalf("list = %d, %v", len(list), err)
	}
	if !list[1].HasAuth || list[1].Host != "books.example.org:8443" || list[0].HasAuth {
		t.Errorf("list = %+v", list[:2])
	}
}

func TestCustomProviderAuditHidesSecret(t *testing.T) {
	e := newLookupEnv(t)
	srv, _ := siteServer(t, `{"matches":[{"title":"The Hobbit"}]}`, 200)
	id, err := e.svc.AddCustomProvider(bg, metadata.CustomProviderInput{Name: "Mine", Address: loopbackAddr(srv) + "/tokenpath123", Auth: secretHeader}, "")
	if err != nil {
		t.Fatal(err)
	}
	job, err := e.svc.StartProviderTest(bg, id, "")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(job); j.State != jobs.StateDone {
		t.Fatalf("test job: %s %s", j.State, j.ErrorCode)
	}
	if err := e.svc.RemoveCustomProvider(bg, id, ""); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT COALESCE(group_concat(detail_json || target || action), '') FROM audit_log`,
		`SELECT COALESCE(group_concat(payload_json || COALESCE(result_json, '') || COALESCE(error_text, '')), '') FROM jobs`,
	} {
		var all string
		if err := e.st.Reader().QueryRow(q).Scan(&all); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"s3cr3t", "tokenpath123", "Bearer"} {
			if strings.Contains(all, bad) {
				t.Errorf("%q leaks %q: %s", q[:30], bad, all)
			}
		}
	}
	for _, action := range []string{"custom_provider.add", "custom_provider.test", "custom_provider.remove"} {
		if n := e.count(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, action); n != 1 {
			t.Errorf("%s audit rows = %d, want 1", action, n)
		}
	}
	var detail string
	if err := e.st.Reader().QueryRow(`SELECT detail_json FROM audit_log WHERE action = 'custom_provider.add'`).Scan(&detail); err != nil ||
		!strings.Contains(detail, `"auth":true`) || !strings.Contains(detail, `"name":"Mine"`) || !strings.Contains(detail, `127.0.0.1:`) {
		t.Errorf("add detail = %s, %v", detail, err)
	}
	if _, err := e.svc.StartProviderTest(bg, id, ""); !errors.Is(err, metadata.ErrProviderNotFound) {
		t.Errorf("test of a removed site: %v", err)
	}
	if err := e.svc.RemoveCustomProvider(bg, id, ""); !errors.Is(err, metadata.ErrProviderNotFound) {
		t.Errorf("second remove: %v", err)
	}
}

func TestCustomProvidersReload(t *testing.T) {
	e := newLookupEnv(t)
	keys := func(s *metadata.Service) string {
		var k []string
		for _, p := range s.LookupPlaces() {
			k = append(k, p.Key+"="+p.Label)
		}
		return strings.Join(k, ",")
	}
	if got := keys(e.svc); got != "openlibrary=Open Library,itunes=Apple Books" {
		t.Fatalf("places = %s", got)
	}
	a, _ := e.svc.AddCustomProvider(bg, metadata.CustomProviderInput{Name: "Alpha", Address: "https://a.example"}, "")
	b, _ := e.svc.AddCustomProvider(bg, metadata.CustomProviderInput{Name: "Beta", Address: "https://b.example"}, "")
	want := "openlibrary=Open Library,itunes=Apple Books,custom:" + a + "=Alpha,custom:" + b + "=Beta"
	if got := keys(e.svc); got != want {
		t.Fatalf("places = %s, want %s", got, want)
	}
	// A service started later reads the table at SetOnline.
	again := metadata.New(e.repo, e.runner, quiet())
	again.SetOnline(online.New(online.Options{CoverDir: t.TempDir(), Transport: e.net}), mediatest.FakeTools(t), e.dataDir)
	if got := keys(again); got != want {
		t.Errorf("after a restart places = %s", got)
	}
	if err := e.svc.RemoveCustomProvider(bg, a, ""); err != nil {
		t.Fatal(err)
	}
	if got := keys(e.svc); strings.Contains(got, "Alpha") || !strings.Contains(got, "Beta") {
		t.Errorf("after remove places = %s", got)
	}
	var none *metadata.Service
	if got := keys(none); got != "openlibrary=Open Library,itunes=Apple Books" {
		t.Errorf("nil service places = %s", got)
	}
}

func TestCleanSourcesCustomKeys(t *testing.T) {
	in := []string{"custom:cp_b", "itunes", "google", "custom:cp_a", "custom:cp_b", "custom:bad key", "audnexus", "openlibrary", "custom:"}
	got := strings.Join(metadata.CleanSources(in), ",")
	if want := "openlibrary,itunes,custom:cp_b,custom:cp_a"; got != want {
		t.Errorf("CleanSources = %s, want %s", got, want)
	}
	var many []string
	for i := 0; i < 14; i++ {
		many = append(many, fmt.Sprintf("custom:cp_%d", i))
	}
	if n := len(metadata.CleanSources(many)); n != 10 {
		t.Errorf("kept %d custom keys, want 10", n)
	}
}

func TestProviderTestJob(t *testing.T) {
	e := newLookupEnv(t)
	ok, _ := siteServer(t, `{"matches":[{"title":"The Hobbit"},{"title":"The Hobbit 2"}]}`, 200)
	empty, _ := siteServer(t, `{"matches":[]}`, 200)
	down, _ := siteServer(t, ``, 500)
	denied, _ := siteServer(t, ``, 401)
	run := func(name string, srv *httptest.Server) metadata.ProviderTestResult {
		t.Helper()
		id, err := e.svc.AddCustomProvider(bg, metadata.CustomProviderInput{Name: name, Address: loopbackAddr(srv), Auth: "x"}, "")
		if err != nil {
			t.Fatal(err)
		}
		job, err := e.svc.StartProviderTest(bg, id, "")
		if err != nil {
			t.Fatal(err)
		}
		j := e.wait(job)
		if j.State != jobs.StateDone {
			t.Fatalf("%s: job %s %s", name, j.State, j.ErrorCode)
		}
		var r metadata.ProviderTestResult
		if err := json.Unmarshal(j.Result, &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := run("Ok", ok); !r.OK || r.Found != 2 || r.Code != "" {
		t.Errorf("ok = %+v", r)
	}
	if r := run("Empty", empty); !r.OK || r.Found != 0 {
		t.Errorf("empty = %+v", r)
	}
	if r := run("Down", down); r.OK || r.Code != "server" {
		t.Errorf("down = %+v", r)
	}
	if r := run("Denied", denied); r.OK || r.Code != "unauthorized" {
		t.Errorf("denied = %+v", r)
	}
}

func TestProviderTestJobReusesRunning(t *testing.T) {
	e := newLookupEnv(t)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		fmt.Fprint(w, `{"matches":[]}`)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	id, err := e.svc.AddCustomProvider(bg, metadata.CustomProviderInput{Name: "Slow", Address: loopbackAddr(srv)}, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.svc.StartProviderTest(bg, id, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.StartProviderTest(bg, id, "")
	if err != nil || second != first {
		t.Errorf("second start = %q, %v; want the running job %q", second, err, first)
	}
	close(release)
	e.wait(first)
}

func TestBulkAsksCustomProvider(t *testing.T) {
	e := newBulkEnv(t)
	srv, asked := siteServer(t, `{"matches":[{"title":"Custom Only Title","author":"Ann Author","publisher":"Shelf House","asin":"B0CUSTOM01"}]}`, 200)
	id, err := e.svc.AddCustomProvider(bg, metadata.CustomProviderInput{Name: "Mine", Address: loopbackAddr(srv)}, "")
	if err != nil {
		t.Fatal(err)
	}
	key := online.CustomKey(id)
	ids := e.addBooks(titles("Custom Only Title")...)
	j := e.finished(e.enqueue(metadata.BulkAll, "audnexus", key))
	if j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	if asked.Load() == 0 {
		t.Fatal("the custom site was never asked")
	}
	if n := e.count(`SELECT COUNT(*) FROM online_usage WHERE provider = ?`, key); n != 0 {
		t.Errorf("online_usage rows for the custom key = %d, want 0", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM online_cache WHERE provider = ?`, key); n != 0 {
		t.Errorf("online_cache rows for the custom key = %d, want 0", n)
	}
	if got := e.outcome(ids[0]); got == "" {
		t.Error("no lookup outcome recorded")
	}
}
