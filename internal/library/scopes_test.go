package library_test

import (
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// Scopes shared by the browse and search tests: everything visible, and nothing visible.
var (
	allScope  = library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "1=1"}}
	noneScope = library.Scope{Books: share.Filter{SQL: "0=1"}, Libraries: share.Filter{SQL: "0=1"}}
)
