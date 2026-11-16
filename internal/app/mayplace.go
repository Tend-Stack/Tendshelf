package app

import (
	"context"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// mayPlace answers upload.Config.MayPlace from the database as it is when placement starts (decision 0029):
// a missing or disabled person places nothing, the owner and admins build at the top of the upload folder,
// a contributor with an active whole-library or whole-server grant builds in their own folder, and anyone
// else (a demoted contributor, a member, a guest) places nothing.
func mayPlace(st *db.Store) func(ctx context.Context, userID, libraryID string) upload.Placement {
	ur, can := users.New(st, users.Options{}), share.New(st)
	return func(ctx context.Context, userID, libraryID string) upload.Placement {
		u, err := ur.ByID(ctx, userID)
		if err != nil || u.Disabled {
			return upload.PlaceRefused
		}
		switch {
		case u.Role == users.RoleOwner || u.Role == users.RoleAdmin:
			return upload.PlaceTop
		case u.Contributor() && can.Can(ctx, u, share.AddBooks, share.Target{Kind: share.TargetLibrary, ID: libraryID, LibraryID: libraryID}):
			return upload.PlaceOwnFolder
		}
		return upload.PlaceRefused
	}
}

// scopeFor answers upload.Config.ScopeFor: the filters of the books and libraries a person may list, built
// exactly as the web layer builds them, so the "already in the library" check names only what they can see.
// A person who does not exist or is switched off lists nothing (false); any other lookup error is returned, so an
// upload is never placed unchecked because the database hiccuped.
func scopeFor(st *db.Store) func(ctx context.Context, userID string) (library.Scope, bool, error) {
	ur, can := users.New(st, users.Options{}), share.New(st)
	return func(ctx context.Context, userID string) (library.Scope, bool, error) {
		u, err := ur.ByID(ctx, userID)
		if errors.Is(err, users.ErrNotFound) {
			return library.Scope{}, false, nil
		}
		if err != nil {
			return library.Scope{}, false, err
		}
		if u.Disabled {
			return library.Scope{}, false, nil
		}
		return library.Scope{Books: can.BookFilter(u, "b"), Libraries: can.LibraryFilter(u, "l"), Shared: can.SharedBooks(u)}, true, nil
	}
}
