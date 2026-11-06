package token

import "testing"

func TestTypableShapeAndSpread(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		k := NewTypable()
		if !IsTypable(k) {
			t.Fatalf("key %q has the wrong shape", k)
		}
		if seen[k] {
			t.Fatalf("key %q repeated", k)
		}
		seen[k] = true
	}
	for _, bad := range []string{"", "abc", "ABCDEFGHIJKLMNOPQRSTUVWX", "abcdefghijklmnopqrstuv01", "abcdefghijklmnopqrstuvw-"} {
		if IsTypable(bad) {
			t.Errorf("%q passed", bad)
		}
	}
}
