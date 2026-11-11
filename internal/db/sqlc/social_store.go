package db

import "context"

// RegisterSocialUserTxParams: an account made with Google or Apple, its
// profile and the identity it signs in with, all or nothing.
type RegisterSocialUserTxParams struct {
	User     RegisterSocialUserParams
	Provider string
	Subject  string
	// the email of the Google or Apple account ("" for none)
	IdentityEmail string
}

func (s *SQLStore) RegisterSocialUserTx(ctx context.Context, arg RegisterSocialUserTxParams) (User, error) {
	var user User
	err := s.execTx(ctx, func(q *Queries) error {
		var err error
		user, err = q.RegisterSocialUser(ctx, arg.User)
		if err != nil {
			return err
		}
		if _, err := q.CreateProfile(ctx, CreateProfileParams{
			UserID:      user.ID,
			FirstName:   arg.User.FirstName,
			LastName:    arg.User.LastName,
			PhoneNumber: arg.User.PhoneNumber,
			Email:       arg.User.Email,
		}); err != nil {
			return err
		}
		return q.CreateUserIdentity(ctx, CreateUserIdentityParams{
			Provider: arg.Provider,
			Subject:  arg.Subject,
			UserID:   user.ID,
			Email:    arg.IdentityEmail,
		})
	})
	return user, err
}
