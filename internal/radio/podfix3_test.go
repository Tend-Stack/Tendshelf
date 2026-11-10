package radio

import "testing"

func TestPodFix3CleanTitlePairs(t *testing.T) {
	real := `Sabrina Carpenter - text="Manchild" song_spot="M" MediaBaseId="3132429" itunesTrackId="0" amgTrackId="-1" amgArtistId="0" TAID="0" TPID="347394500" cartcutId="0443441001" amgArtworkURL="https://i.ihea`
	for _, c := range []struct{ name, in, want string }{
		{"iheart", real, "Sabrina Carpenter - Manchild"},
		{"unterminated text", `Artist - text="Man`, "Artist - Man"},
		{"truncated after text", `Artist - text="Song" song_spot="M" MediaBa`, "Artist - Song"},
		{"ordinary", "Artist - Song", "Artist - Song"},
		{"real name with equals quote", `Artist - Song a="b" c`, `Artist - Song a="b" c`},
		{"non-latin", `Björk - text="Jóga ありがとう" song_spot="M" x="1"`, "Björk - Jóga ありがとう"},
		{"advert", `Station - text="Buy now" song_spot="T" MediaBaseId="1"`, ""},
		{"empty text", `Station - text="" song_spot="M"`, ""},
		{"only pairs", `text="X" song_spot="T"`, ""},
		{"only pairs no text", `MediaBaseId="1" TAID="0"`, ""},
		{"trailing dash", "Artist - ", "Artist"},
		{"trailing dash after song", "Artist - Song - ", "Artist - Song"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := CleanTitle(c.in); got != c.want {
				t.Fatalf("CleanTitle(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
