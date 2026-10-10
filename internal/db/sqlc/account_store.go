package db

import (
	"context"
	"database/sql"
	"errors"
)

// ErrLastSuperadmin: the only superadmin can't delete their account, the app
// would have nobody left to give the roles.
var ErrLastSuperadmin = errors.New("the last superadmin can't delete their account")

// DeleteAccountTx deletes a member and everything that is theirs, in one
// transaction. The database does most of it (ON DELETE CASCADE: profile,
// personal items, authorities, event registrations; SET NULL: the events,
// news and images they published stay for the community). Two things go
// first: their reports, whose entries hold their personal items, and the
// authorities of other members that point to them.
func (s *SQLStore) DeleteAccountTx(ctx context.Context, userID int64) error {
	return s.execTx(ctx, func(q *Queries) error {
		user, err := q.GetUserById(ctx, userID)
		if err != nil {
			return err
		}
		if user.Role == "superadmin" {
			count, err := q.CountSuperadmins(ctx)
			if err != nil {
				return err
			}
			if count <= 1 {
				return ErrLastSuperadmin
			}
		}
		if err := q.DeleteUserReports(ctx, userID); err != nil {
			return err
		}
		if err := q.DeleteAuthoritiesLinkedTo(ctx, sql.NullInt64{Int64: userID, Valid: true}); err != nil {
			return err
		}
		return q.DeleteUser(ctx, userID)
	})
}
