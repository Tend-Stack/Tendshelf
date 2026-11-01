package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// accessMux serves two probe routes behind the real session middleware, so the helpers run with a
// real signed-in person.
func accessMux(a *testApp) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /probe/server", func(w http.ResponseWriter, r *http.Request) {
		if a.srv.allow(w, r, share.ManageServer, share.Target{Kind: share.TargetServer}) {
			_, _ = w.Write([]byte("allowed"))
		}
	})
	mux.HandleFunc("GET /probe/book/{id}", func(w http.ResponseWriter, r *http.Request) {
		if b, ok := a.srv.loadBook(w, r, r.PathValue("id"), share.ViewBook); ok {
			_, _ = w.Write([]byte("book " + b.Title))
		}
	})
	return auth.Middleware(a.srv.sessions, a.srv.proxies)(mux)
}

func probe(h http.Handler, path string, htmx bool, c *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNewHandlerRequiresLibraryDataDirJobs(t *testing.T) {
	a := newTestApp(t)
	full := Options{Store: a.st, SetupCode: "X", Library: a.lib, DataDir: a.dir, Jobs: a.runner}
	if _, err := NewHandler(context.Background(), full); err != nil {
		t.Fatalf("complete options: %v", err)
	}
	for name, mod := range map[string]func(*Options){
		"Library": func(o *Options) { o.Library = nil },
		"DataDir": func(o *Options) { o.DataDir = "" },
		"Jobs":    func(o *Options) { o.Jobs = nil },
	} {
		o := full
		mod(&o)
		_, err := NewHandler(context.Background(), o)
		if err == nil || !strings.Contains(err.Error(), "Options."+name+" is required") {
			t.Errorf("without %s: err = %v", name, err)
		}
	}
}

func TestAllowRefusesWith404(t *testing.T) {
	a := newTestApp(t)
	h := accessMux(a)
	member := a.as(t, users.RoleMember)

	rec := probe(h, "/probe/server", false, member)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("page: %d %q", rec.Code, rec.Body.String())
	}
	rec = probe(h, "/probe/server", true, member)
	body := rec.Body.String()
	if rec.Code != http.StatusNotFound || !strings.Contains(body, `role="alert"`) || strings.Contains(body, "<html") {
		t.Errorf("htmx: %d %q", rec.Code, body)
	}
	if rec = probe(h, "/probe/server", false, a.as(t, users.RoleOwner)); rec.Code != http.StatusOK || rec.Body.String() != "allowed" {
		t.Errorf("owner: %d %q", rec.Code, rec.Body.String())
	}
}

func TestLoadBook(t *testing.T) {
	a := newTestApp(t)
	h := accessMux(a)
	_, f := librarytest.NewLibrary(t, a.lib, "Books", "/audio")
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Found"})

	owner, member := a.as(t, users.RoleOwner), a.as(t, users.RoleMember)
	if rec := probe(h, "/probe/book/"+b.ID, false, owner); rec.Code != http.StatusOK || rec.Body.String() != "book Found" {
		t.Errorf("owner: %d %q", rec.Code, rec.Body.String())
	}
	if rec := probe(h, "/probe/book/"+b.ID, false, member); rec.Code != http.StatusNotFound {
		t.Errorf("member: %d", rec.Code)
	}
	if rec := probe(h, "/probe/book/bk_nope", false, owner); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id: %d", rec.Code)
	}
}

func TestCoverURL(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef"
	for _, c := range []struct {
		book, sha string
		w         int
		want      string
	}{
		{"bk_1", sha, 0, "/covers/bk_1?v=01234567"},
		{"bk_1", sha, 320, "/covers/bk_1?w=320&v=01234567"},
		{"bk_1", "abc", 96, "/covers/bk_1?w=96&v=abc"},
		{"bk_1", "", 320, ""},
	} {
		if got := coverURL(c.book, c.sha, c.w); got != c.want {
			t.Errorf("coverURL(%q, %q, %d) = %q, want %q", c.book, c.sha, c.w, got, c.want)
		}
	}
}

func TestAsMember(t *testing.T) {
	a := newTestApp(t)
	member := a.as(t, users.RoleMember)
	if rec := a.do(http.MethodGet, "/", nil, nil, member); rec.Code != http.StatusOK {
		t.Fatalf("member GET /: %d", rec.Code)
	}
	if other := a.as(t, users.RoleMember); other.Value == member.Value {
		t.Error("two members share a session")
	}
	rec := probe(accessMux(a), "/probe/server", false, member)
	if rec.Code != http.StatusNotFound {
		t.Errorf("allow for member: %d", rec.Code)
	}
}
