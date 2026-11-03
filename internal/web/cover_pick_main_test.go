package web

import (
	"os"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// TestMain lets this test binary stand in for ffmpeg and ffprobe when a test runs the cover
// adoption job (the thumbnails of an uploaded or chosen cover). Without the request it does nothing.
func TestMain(m *testing.M) {
	mediatest.RunFakeIfRequested()
	os.Exit(m.Run())
}
