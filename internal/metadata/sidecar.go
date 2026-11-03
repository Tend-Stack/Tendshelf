// Ports the Extractor extractor_details.py (sidecars, parse_nfo, parse_opf, parse_metadata_json, local_details step 3); see NOTICES.md.

package metadata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// sidecarMaxBytes is the biggest sidecar file that is read; a larger one is ignored.
const sidecarMaxBytes = 1 << 20

// sidecarDescriptionLimit cuts descriptions that come from sidecar files, as the Extractor does.
const sidecarDescriptionLimit = 2000

// sidecarFiles holds the absolute paths of a book's sidecar files; "" means the file is absent.
// They are only ever read.
type sidecarFiles struct{ JSON, Desc, Reader, OPF, NFO string }

// findSidecars looks for a book's sidecars. For a folder book it lists rootPath and matches the
// names in lower case: metadata.json, desc.txt and reader.txt exactly, and the first .opf and the
// first .nfo in name order. For a single-file book (rootPath is the file) it looks for
// <stem>.opf, <stem>.nfo and <stem>.metadata.json beside it, so another book's sidecar in the same
// folder is never taken. Only regular files count: a symlink or a directory is skipped.
func findSidecars(rootPath string, single bool) sidecarFiles {
	var f sidecarFiles
	if single {
		stem := strings.TrimSuffix(rootPath, filepath.Ext(rootPath))
		for _, c := range []struct {
			dst *string
			ext string
		}{{&f.OPF, ".opf"}, {&f.NFO, ".nfo"}, {&f.JSON, ".metadata.json"}} {
			if fi, err := os.Lstat(stem + c.ext); err == nil && fi.Mode().IsRegular() {
				*c.dst = stem + c.ext
			}
		}
		return f
	}
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		return f
	}
	type named struct{ lower, real string }
	var files []named
	for _, e := range entries {
		if e.Type().IsRegular() {
			files = append(files, named{strings.ToLower(e.Name()), e.Name()})
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].lower < files[j].lower })
	for _, n := range files {
		p := filepath.Join(rootPath, n.real)
		switch {
		case n.lower == "metadata.json":
			f.JSON = p
		case n.lower == "desc.txt":
			f.Desc = p
		case n.lower == "reader.txt":
			f.Reader = p
		case strings.HasSuffix(n.lower, ".opf") && f.OPF == "":
			f.OPF = p
		case strings.HasSuffix(n.lower, ".nfo") && f.NFO == "":
			f.NFO = p
		}
	}
	return f
}

// readSidecar returns a sidecar file as text, or "" when it is not a regular file, is larger than
// 1 MiB, or cannot be read. Bytes that are not UTF-8 become U+FFFD.
func readSidecar(path string) string {
	b := readSidecarBytes(path)
	return strings.ToValidUTF8(string(b), "�")
}

func readSidecarBytes(path string) []byte {
	if path == "" {
		return nil
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > sidecarMaxBytes {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) > sidecarMaxBytes {
		return nil
	}
	return b
}

// fromSidecars reads the sidecar files and returns what each source says, under the keys json,
// opf, txt and nfo. A source whose file is absent or unreadable maps to empty Details, so the job
// removes that source's old rows.
func fromSidecars(f sidecarFiles) map[string]Details {
	out := map[string]Details{
		SourceJSON: {}, SourceOPF: {}, SourceTxt: {}, SourceNFO: {},
	}
	if s := readSidecar(f.JSON); s != "" {
		out[SourceJSON] = parseMetadataJSON([]byte(s))
	}
	if s := readSidecar(f.OPF); s != "" {
		out[SourceOPF] = parseOPF([]byte(s))
	}
	if s := readSidecar(f.NFO); s != "" {
		out[SourceNFO] = parseNFO(s)
	}
	var txt Details
	txt.Description = cleanText(readSidecar(f.Desc), sidecarDescriptionLimit)
	for _, line := range strings.Split(readSidecar(f.Reader), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			txt.Narrators = ParseNames(line)
			break
		}
	}
	out[SourceTxt] = txt
	for src, d := range out {
		d.fillProduction()
		out[src] = d
	}
	return out
}

// sidecarSignature changes when a sidecar file is added, removed or changed: a SHA-256 over the
// sorted kind, base name, size and modification time of each. It is "" when there is none.
func sidecarSignature(f sidecarFiles) string {
	var lines []string
	for _, c := range []struct{ kind, path string }{
		{"json", f.JSON}, {"desc", f.Desc}, {"reader", f.Reader}, {"opf", f.OPF}, {"nfo", f.NFO},
	} {
		if c.path == "" {
			continue
		}
		fi, err := os.Lstat(c.path)
		if err != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\x00%s\x00%d\x00%d", c.kind, filepath.Base(c.path), fi.Size(), fi.ModTime().UnixNano()))
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

var (
	nfoLineRe     = regexp.MustCompile(`(?i)^\s*book description\s*$`)
	nfoRuleRe     = regexp.MustCompile(`^\s*=+\s*$`)
	fourDigitsRe  = regexp.MustCompile(`\d{4}`)
	nfoYearKeys   = []string{"copyright", "audible.com release", "audiobook copyright", "book copyright", "recording copyright", "release date", "date"}
	nfoLineSplit  = regexp.MustCompile(`\r\n|\r|\n`)
	commaSplitter = regexp.MustCompile(`\s*,\s*`)
)

// parseNFO reads the "Key: value" lines of an .nfo file and the "Book Description" block.
func parseNFO(text string) Details {
	var d Details
	var desc []string
	var seriesName, seriesSeq string // the two lines may come in either order
	inside := false
	for _, line := range nfoLineSplit.Split(text, -1) {
		if inside {
			if !nfoRuleRe.MatchString(line) {
				desc = append(desc, line)
			}
			continue
		}
		if nfoLineRe.MatchString(line) {
			inside = true
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value := strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		if value == "" {
			continue
		}
		switch {
		case key == "title":
			if t, st, ok := strings.Cut(value, ": "); ok {
				d.Title, d.Subtitle = strings.TrimSpace(t), strings.TrimSpace(st)
			} else {
				d.Title = value
			}
		case key == "author":
			d.Authors = ParseNames(value)
		case key == "narrator" || key == "read by":
			d.Narrators = ParseNames(value)
		case key == "series name":
			seriesName = value
		case key == "genre":
			d.Genres = nil
			for _, g := range commaSplitter.Split(value, -1) {
				if g = strings.TrimSpace(g); g != "" {
					d.Genres = append(d.Genres, g)
				}
			}
		case slices.Contains(nfoYearKeys, key):
			if years := fourDigitsRe.FindAllString(value, -1); len(years) > 0 {
				d.Year = cleanYear(years[len(years)-1])
			}
		case key == "position in series":
			seriesSeq = cleanSequence(value)
		case key == "unabridged":
			d.Abridged = boolp(strings.ToLower(value) != "yes")
		case key == "abridged":
			d.Abridged = boolp(strings.ToLower(value) != "no")
		case key == "publisher":
			d.Publisher = value
		case key == "asin":
			d.ASIN = cleanASIN(value)
		case key == "isbn" || key == "isbn-10" || key == "isbn-13":
			d.ISBN = cleanISBN(value)
		case key == "language" || key == "lang":
			d.Language = value
		}
	}
	if seriesName != "" {
		d.Series = []SeriesRef{{Name: seriesName, Sequence: seriesSeq}}
	}
	if len(desc) > 0 {
		d.Description = cleanText(strings.Join(desc, "\n"), sidecarDescriptionLimit)
	}
	return d
}

// opfNode is one child of the OPF <metadata> element: its local name, attributes (namespaces
// dropped, so opf:role and role are the same) and its own text.
type opfNode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
}

func (n opfNode) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

type opfPackage struct {
	Metadata []struct {
		Nodes []opfNode `xml:",any"`
	} `xml:"metadata"`
}

type opfPerson struct{ name, id string }

// parseOPF reads a Calibre or Audiobookshelf .opf file. encoding/xml never fetches external
// entities, and the decoder is strict; a file that is not well-formed gives empty Details.
func parseOPF(data []byte) Details {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = true
	var pkg opfPackage
	if err := dec.Decode(&pkg); err != nil || len(pkg.Metadata) == 0 {
		return Details{}
	}
	var (
		texts              = map[string]string{}
		authors, narrators []opfPerson
		subjects           []string
		ids                = map[string]string{}
		metas              []opfNode
		refines            = map[string]map[string]string{}
	)
	for _, n := range pkg.Metadata[0].Nodes {
		name := n.XMLName.Local
		val := strings.TrimSpace(n.Text)
		switch {
		case name == "creator" && val != "":
			switch n.attr("role") {
			case "nrt":
				narrators = append(narrators, opfPerson{val, n.attr("id")})
			case "aut", "":
				authors = append(authors, opfPerson{val, n.attr("id")})
			}
		case name == "identifier" && val != "":
			scheme := strings.ToUpper(n.attr("scheme"))
			ids[scheme] = val
			if scheme == "" && strings.HasPrefix(strings.ToLower(val), "urn:isbn:") {
				ids["ISBN"] = val[len("urn:isbn:"):]
			}
		case name == "subject" && val != "":
			subjects = append(subjects, val)
		case name == "meta":
			if ref := n.attr("refines"); ref != "" {
				ref = strings.TrimPrefix(ref, "#")
				if refines[ref] == nil {
					refines[ref] = map[string]string{}
				}
				refines[ref][n.attr("property")] = val
			} else {
				metas = append(metas, n)
			}
		case val != "":
			switch name {
			case "title", "subtitle", "date", "publisher", "description", "language":
				if texts[name] == "" {
					texts[name] = val
				}
			}
		}
	}
	// An author that a refines record marks as a narrator is a narrator.
	var onlyAuthors []opfPerson
	for _, a := range authors {
		if a.id != "" && refines[a.id]["role"] == "nrt" {
			narrators = append(narrators, a)
		} else {
			onlyAuthors = append(onlyAuthors, a)
		}
	}
	authors = onlyAuthors
	if len(narrators) == 0 {
		for _, m := range metas {
			if m.attr("name") != "calibre:user_metadata:#narrators" {
				continue
			}
			var um struct {
				Value []string `json:"#value#"`
			}
			content := strings.ReplaceAll(m.attr("content"), "&quot;", `"`)
			if json.Unmarshal([]byte(content), &um) == nil {
				for _, v := range um.Value {
					if v = strings.TrimSpace(v); v != "" {
						narrators = append(narrators, opfPerson{name: v})
					}
				}
			}
		}
	}
	var series, seq string
	for i, m := range metas {
		if m.attr("name") != "calibre:series" || strings.TrimSpace(m.attr("content")) == "" {
			continue
		}
		series = strings.TrimSpace(m.attr("content"))
		if i+1 < len(metas) && metas[i+1].attr("name") == "calibre:series_index" {
			seq = strings.TrimSpace(metas[i+1].attr("content"))
		}
		break
	}
	if series != "" && seq == "" {
		for _, m := range metas {
			if m.attr("name") == "calibre:series_index" {
				seq = strings.TrimSpace(m.attr("content"))
				break
			}
		}
	}

	names := func(ps []opfPerson) []string {
		var out []string
		for _, p := range ps {
			if !slices.Contains(out, p.name) {
				out = append(out, p.name)
			}
		}
		return out
	}
	desc := strings.NewReplacer("&lt;", "<", "&gt;", ">").Replace(texts["description"])
	date := []rune(texts["date"])
	if len(date) > 4 {
		date = date[:4]
	}
	d := Details{
		Title: texts["title"], Subtitle: texts["subtitle"],
		Authors: names(authors), Narrators: names(narrators),
		Year: cleanYear(string(date)), Publisher: texts["publisher"],
		ISBN: cleanISBN(ids["ISBN"]), ASIN: cleanASIN(ids["ASIN"]),
		Description: cleanText(desc, sidecarDescriptionLimit),
		Language:    texts["language"],
	}
	for _, s := range subjects {
		if !slices.Contains(d.Genres, s) {
			d.Genres = append(d.Genres, s)
		}
	}
	if series != "" {
		d.Series = []SeriesRef{{Name: series, Sequence: cleanSequence(seq)}}
	}
	return d
}

// parseMetadataJSON reads Audiobookshelf's metadata.json; old files nest the details under
// "metadata". Anything that is not a JSON object gives empty Details.
func parseMetadataJSON(data []byte) Details {
	var top any
	if json.Unmarshal(data, &top) != nil {
		return Details{}
	}
	obj, ok := top.(map[string]any)
	if !ok {
		return Details{}
	}
	if nested, ok := obj["metadata"].(map[string]any); ok {
		merged := map[string]any{}
		for k, v := range obj {
			merged[k] = v
		}
		for k, v := range nested {
			merged[k] = v
		}
		obj = merged
	}
	list := func(k string) []string {
		var out []string
		arr, _ := obj[k].([]any)
		for _, v := range arr {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	// text accepts a string or a number (a year may be written either way), never a bool.
	text := func(k string) string {
		switch v := obj[k].(type) {
		case string:
			return cleanText(v, 0)
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
		return ""
	}
	d := Details{
		Title: text("title"), Subtitle: text("subtitle"),
		Authors: list("authors"), Narrators: list("narrators"), Genres: list("genres"),
		Publisher: text("publisher"), ISBN: cleanISBN(text("isbn")), ASIN: cleanASIN(text("asin")),
		Language: text("language"),
	}
	year := text("publishedYear")
	if year == "" {
		year = text("publishedDate")
	}
	d.Year = cleanYear(year)
	if s, ok := obj["description"].(string); ok {
		d.Description = cleanText(s, sidecarDescriptionLimit)
	}
	for _, s := range list("series") {
		if name, seq, ok := ParseSeriesString(s); ok && name != "" {
			if !slices.ContainsFunc(d.Series, func(r SeriesRef) bool { return r.Name == name }) {
				d.Series = append(d.Series, SeriesRef{Name: name, Sequence: cleanSequence(seq)})
			}
		}
	}
	switch v := obj["abridged"].(type) {
	case bool:
		d.Abridged = boolp(v)
	case string:
		switch strings.ToLower(v) {
		case "true":
			d.Abridged = boolp(true)
		case "false":
			d.Abridged = boolp(false)
		}
	}
	return d
}
