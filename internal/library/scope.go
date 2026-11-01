package library

import (
	"regexp"

	"github.com/Tend-Stack/tendshelf/internal/share"
)

// Scope is what the person asking may see. Every listing query joins its filters with the fixed
// aliases b (books) and l (libraries): Books is built with alias "b", Libraries with alias "l".
type Scope struct{ Books, Libraries share.Filter }

var listableAliasRE = regexp.MustCompile(`^[a-z_]{1,16}$`)

// Listable is the SQL condition for a book that browse lists show: not refused and not missing.
// alias is the books table alias; anything but lowercase letters and underscores panics.
func Listable(alias string) string {
	if !listableAliasRE.MatchString(alias) {
		panic("library: unsafe SQL alias " + alias)
	}
	return alias + ".refused_reason IS NULL AND " + alias + ".missing_since IS NULL"
}
