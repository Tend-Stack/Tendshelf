package metadata

import (
	"encoding/json"
	"sort"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// rank is a source's place in Precedence (0 = strongest); unknown sources rank last.
func rank(source string) int {
	for i, s := range Precedence {
		if s == source {
			return i
		}
	}
	return len(Precedence)
}

// Resolve picks, for every field, the value of the strongest source, and says which source that
// was. A "user" row wins even when its value is empty: the owner cleared the field on purpose.
// A row that cannot be read as its field's type is ignored.
func Resolve(rows []library.MetadataValue) (Details, map[string]string) {
	byField := map[string][]library.MetadataValue{}
	for _, r := range rows {
		byField[r.Field] = append(byField[r.Field], r)
	}
	var d Details
	from := map[string]string{}
	for field, rs := range byField {
		sort.SliceStable(rs, func(i, j int) bool { return rank(rs[i].Source) < rank(rs[j].Source) })
		for _, r := range rs {
			if apply(&d, field, r.Value) {
				from[field] = r.Source
				break
			}
		}
	}
	return d, from
}

// apply decodes one value into its field of d and reports whether it could.
func apply(d *Details, field string, raw json.RawMessage) bool {
	str := func(dst *string) bool { return json.Unmarshal(raw, dst) == nil }
	list := func(dst *[]string) bool { return json.Unmarshal(raw, dst) == nil }
	flag := func(dst **bool) bool {
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return false
		}
		*dst = &b
		return true
	}
	switch field {
	case FieldTitle:
		return str(&d.Title)
	case FieldSubtitle:
		return str(&d.Subtitle)
	case FieldDescription:
		return str(&d.Description)
	case FieldPublisher:
		return str(&d.Publisher)
	case FieldLanguage:
		return str(&d.Language)
	case FieldISBN:
		return str(&d.ISBN)
	case FieldASIN:
		return str(&d.ASIN)
	case FieldPublishedYear:
		return json.Unmarshal(raw, &d.Year) == nil
	case FieldAuthors:
		return list(&d.Authors)
	case FieldNarrators:
		return list(&d.Narrators)
	case FieldGenres:
		return list(&d.Genres)
	case FieldSeries:
		return json.Unmarshal(raw, &d.Series) == nil
	case FieldAbridged:
		return flag(&d.Abridged)
	case FieldDramatized:
		return flag(&d.Dramatized)
	case FieldExplicit:
		return flag(&d.Explicit)
	}
	return false
}
