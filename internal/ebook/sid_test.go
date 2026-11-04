package ebook

import "testing"

func TestParseSID(t *testing.T) {
	cases := []struct {
		sid      string
		ch, p, s int
		ok       bool
	}{
		{"c1.p1.s0", 1, 1, 0, true},
		{"c12.p345.s6789", 12, 345, 6789, true},
		{"c99999.p999999.s99999", 99999, 999999, 99999, true},
		{"c0.p1.s0", 0, 0, 0, false},
		{"c1.p0.s0", 0, 0, 0, false},
		{"c1.p1.s01", 0, 0, 0, false},
		{"c100000.p1.s0", 0, 0, 0, false},
		{"c1.p1000000.s0", 0, 0, 0, false},
		{"c1.p1.s100000", 0, 0, 0, false},
		{"c1.p1", 0, 0, 0, false},
		{"c1.p1.s0.x", 0, 0, 0, false},
		{"c1.p1.s", 0, 0, 0, false},
		{"c1.p1.s-1", 0, 0, 0, false},
		{"c+1.p1.s0", 0, 0, 0, false},
		{"x1.p1.s0", 0, 0, 0, false},
		{"c1.p1.s٣", 0, 0, 0, false},
		{"", 0, 0, 0, false},
	}
	for _, c := range cases {
		ch, p, s, ok := ParseSID(c.sid)
		if ok != c.ok || ch != c.ch || p != c.p || s != c.s {
			t.Errorf("ParseSID(%q) = %d %d %d %v", c.sid, ch, p, s, ok)
		}
	}
	if ch, p, s, ok := ParseSID(SentenceID("c3.p4", 5)); !ok || ch != 3 || p != 4 || s != 5 {
		t.Errorf("SentenceID round trip = %d %d %d %v", ch, p, s, ok)
	}
}

func TestParseSIDCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"c1.p1.s0", "c1.p1.s0", 0},
		{"c1.p1.s2", "c1.p1.s10", -1},
		{"c1.p9.s0", "c1.p10.s0", -1},
		{"c2.p1.s0", "c1.p99.s9", 1},
		{"bad", "c1.p1.s0", -1},
		{"c1.p1.s0", "bad", 1},
	}
	for _, c := range cases {
		if got := CompareSID(c.a, c.b); got != c.want {
			t.Errorf("CompareSID(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
