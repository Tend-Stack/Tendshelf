package online

// Book is one candidate from a provider. Values come back raw (HTML and "Book 2" stay as the
// site sent them); the metadata package cleans them.
type Book struct {
	Provider      string
	ProviderLabel string
	ID            string
	Title         string
	Subtitle      string
	Publisher     string
	Description   string
	Language      string
	ISBN          string
	ASIN          string
	CoverURL      string
	Sequence      string
	SeriesName    string
	Format        string
	Rating        string
	RatingCount   string
	Year          int
	Authors       []Person
	Narrators     []string
	Genres        []string
	Score         float64
	Exact         bool
}

// Person is an author with the Audible ASIN when the site gives one.
type Person struct{ Name, ASIN string }

var providerLabels = map[string]string{
	"audnexus":    "Audible (via Audnexus)",
	"openlibrary": "Open Library",
	"itunes":      "Apple Books",
}

func label(provider string) string {
	if l, ok := providerLabels[provider]; ok {
		return l
	}
	return provider
}
