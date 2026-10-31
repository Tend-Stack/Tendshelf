package db

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCheckHeader(t *testing.T) {
	cases := []struct {
		name string
		h    Header
		want error
	}{
		{"fresh file", Header{0, 0}, nil},
		{"current", Header{ApplicationID, 3}, nil},
		{"older schema migrates", Header{ApplicationID, 1}, nil},
		{"newer schema refused", Header{ApplicationID, 4}, ErrSchemaTooNew},
		{"other program", Header{0x12345678, 0}, ErrNotTendshelf},
		{"unmarked but versioned", Header{0, 7}, ErrNotTendshelf},
		{"negative version", Header{ApplicationID, -1}, ErrNotTendshelf},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckHeader(c.h, 3)
			if !errors.Is(err, c.want) {
				t.Fatalf("CheckHeader(%+v) = %v, want %v", c.h, err, c.want)
			}
			var se *SchemaError
			if c.want != nil && (!errors.As(err, &se) || se.Supported != 3) {
				t.Fatalf("want *SchemaError with Supported=3, got %#v", err)
			}
		})
	}
}

func TestNewID(t *testing.T) {
	id := NewID("bk")
	if !strings.HasPrefix(id, "bk_") || len(id) != 3+26 {
		t.Fatalf("bad id %q", id)
	}
	for _, r := range id[3:] {
		if !strings.ContainsRune(crockford, r) {
			t.Fatalf("id %q has %q outside the Crockford alphabet", id, r)
		}
	}
	seen := map[string]bool{}
	for range 1000 {
		v := NewID("x")
		if seen[v] {
			t.Fatalf("duplicate id %q", v)
		}
		seen[v] = true
	}
}

func TestNewIDSortsByTime(t *testing.T) {
	t0 := time.UnixMilli(1_700_000_000_000)
	a := newID("bk", t0)
	b := newID("bk", t0.Add(time.Millisecond))
	if !(a < b) {
		t.Fatalf("ids not time-ordered: %q !< %q", a, b)
	}
	// The first 10 characters encode the timestamp only.
	if newID("bk", t0)[:3+10] != a[:3+10] {
		t.Fatal("timestamp prefix not stable")
	}
}
