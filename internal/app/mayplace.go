package app

import (
	"context"

	"github.com/Tend-Stack/tendshelf/internal/db"
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
