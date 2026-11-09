package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The Dockerfile starts the server with flags; the real parser must accept every one, so the image
// cannot drift from main.go. "-version" is appended so run parses and returns without serving.
func TestDockerfileFlagsExist(t *testing.T) {
	src, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	for _, line := range strings.Split(string(src), "\n") {
		if !strings.HasPrefix(line, "CMD [") && !strings.HasPrefix(line, "ENTRYPOINT [") {
			continue
		}
		var list []string
		if err := json.Unmarshal([]byte(line[strings.Index(line, "["):]), &list); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if strings.HasPrefix(line, "ENTRYPOINT") {
			continue // the program name, not a flag
		}
		args = append(args, list...)
	}
	if len(args) == 0 || !strings.HasPrefix(args[0], "-") {
		t.Fatalf("expected the CMD to pass flags, got %v", args)
	}
	if err := run(context.Background(), append(args, "-version")); err != nil {
		t.Errorf("the server rejects the Dockerfile's CMD %v: %v", args, err)
	}
}
