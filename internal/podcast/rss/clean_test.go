package rss

import (
	"strings"
	"testing"
)

const rel = ` rel="nofollow noopener noreferrer ugc"`

func TestCleanHTML(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "  ", ""},
		{"allowed kept", `<p>a <strong>b</strong> <em>c</em> <i>d</i> <b>e</b></p><ul><li>x</li></ul><ol><li>y</li></ol><blockquote>q</blockquote>a<br/>b`,
			`<p>a <strong>b</strong> <em>c</em> <i>d</i> <b>e</b></p><ul><li>x</li></ul><ol><li>y</li></ol><blockquote>q</blockquote>a<br>b`},
		{"attributes stripped", `<p class="x" style="color:red" onclick="y()">t</p>`, `<p>t</p>`},
		{"script dropped with content", `<p>a</p><script>alert(1)</script><p>b</p>`, `<p>a</p><p>b</p>`},
		{"style and iframe", `x<style>p{}</style><iframe src="//e"><p>inner</p></iframe>y`, `xy`},
		{"unknown tags keep text", `<div><span>hello</span> <h2>big</h2></div>`, `hello big`},
		{"link ok", `<a href="https://e.example/a?b=1&c=2" target="_blank" onclick="x">go</a>`, `<a href="https://e.example/a?b=1&amp;c=2"` + rel + `>go</a>`},
		{"mailto ok", `<a href="mailto:me@e.example">mail</a>`, `<a href="mailto:me@e.example"` + rel + `>mail</a>`},
		{"javascript dropped", `<a href="javascript:alert(1)">x</a> <a href=" JaVaScRiPt:alert(1)">y</a>`, `x y`},
		{"data and relative dropped", `<a href="data:text/html,hi">x</a><a href="/rel">y</a><a>z</a>`, `xyz`},
		{"control chars in href", "<a href=\"java\tscript:alert(1)\">x</a>", `x`},
		{"image dropped", `<img src="https://e.example/a.png" onerror="x">t`, `t`},
		{"unbalanced closed", `<p><strong>open`, `<p><strong>open</strong></p>`},
		{"stray end ignored", `a</p></strong>b`, `ab`},
		{"entities escaped again", `<p>1 &lt; 2 &amp; "3"</p>`, `<p>1 &lt; 2 &amp; &#34;3&#34;</p>`},
		{"comment dropped", `a<!-- <script> -->b <p>c</p>`, `ab <p>c</p>`},
		{"plain paragraphs", "one\nline two\n\nsecond", `<p>one<br>line two</p><p>second</p>`},
		{"plain autolink", "see https://e.example/a?x=1&y=2. And http://f.example, ok", `<p>see <a href="https://e.example/a?x=1&amp;y=2"` + rel + `>https://e.example/a?x=1&amp;y=2</a>. And <a href="http://f.example"` + rel + `>http://f.example</a>, ok</p>`},
		{"plain no ftp link", "ftp://x.example and www.example.com", `<p>ftp://x.example and www.example.com</p>`},
		{"plain escapes", "a & b > c", `<p>a &amp; b &gt; c</p>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CleanHTML(c.in, NotesMax); got != c.want {
				t.Errorf("\n got %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestCleanHTMLCap(t *testing.T) {
	in := "<p>" + strings.Repeat("word <b>bold</b> ", 20000) + "</p>"
	out := CleanHTML(in, NotesMax)
	if len(out) > NotesMax || len(out) < NotesMax-200 {
		t.Fatalf("len %d", len(out))
	}
	if !strings.HasSuffix(out, "</p>") || strings.Count(out, "<b>") != strings.Count(out, "</b>") {
		t.Errorf("tail %q", out[len(out)-40:])
	}
	plain := CleanHTML(strings.Repeat("é", 100000), 1000)
	if len(plain) > 1000 || !strings.HasPrefix(plain, "<p>") || !strings.HasSuffix(plain, "</p>") {
		t.Errorf("plain cap: %d", len(plain))
	}
	many := CleanHTML(strings.Repeat("para\n\n", 20000), 1000)
	if len(many) > 1000 || !strings.HasSuffix(many, "</p>") {
		t.Errorf("paragraph cap: %d", len(many))
	}
	deep := CleanHTML(strings.Repeat("<b>", 5000), 1000)
	if len(deep) > 1000 {
		t.Errorf("deep: %d", len(deep))
	}
}
