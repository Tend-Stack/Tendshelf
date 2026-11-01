package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestToastOOB(t *testing.T) {
	tests := []struct {
		kind    string
		want    []string
		notWant []string
	}{
		{"ok", []string{`role="status"`, `ts-toast__ok`, `<svg`, `hx-swap-oob="beforeend:#toasts"`}, []string{`ts-toast--hint`, `role="alert"`}},
		{"hint", []string{`role="status"`, `ts-toast--hint`}, []string{`<svg`, `ts-toast__ok`, `ts-toast__err`}},
		{"err", []string{`role="alert"`, `ts-toast__err`, `<svg`}, []string{`role="status"`, `ts-toast--hint`}},
	}
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			var buf bytes.Buffer
			if err := ToastOOB(tc.kind, "Bookmarked <b>at</b> 14:06").Render(context.Background(), &buf); err != nil {
				t.Fatal(err)
			}
			got := buf.String()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in %s", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("unexpected %q in %s", w, got)
				}
			}
			if strings.Contains(got, "<b>") {
				t.Errorf("message not escaped: %s", got)
			}
		})
	}
}
