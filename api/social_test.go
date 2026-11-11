package api

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/socialauth"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/stretchr/testify/require"
)

// fakeIdentities: the token → the Google or Apple account Firebase would
// have checked.
type fakeIdentities map[string]socialauth.Identity

func (f fakeIdentities) Identity(_ context.Context, idToken string) (socialauth.Identity, error) {
	if identity, ok := f[idToken]; ok {
		return identity, nil
	}
	return socialauth.Identity{}, socialauth.ErrInvalid
}

func newSocialRouter(t *testing.T, identities socialauth.Verifier) http.Handler {
	server, err := New(testStore, &utils.Config{Token: utils.TokenConfig{SecretKey: strings.Repeat("k", 32), Duration: "15m"}})
	require.NoError(t, err)
	server.identities = identities
	return server.SetupRouter()
}

func googleAccount(email string) socialauth.Identity {
	return socialauth.Identity{
		Provider:      socialauth.Google,
		Subject:       fmt.Sprintf("uid-%d", rand.Int64()),
		Email:         email,
		EmailVerified: true,
		Name:          "Paul Ngono",
	}
}

func uniqueEmail() string { return fmt.Sprintf("m%d@example.com", rand.Int64()) }

func uniquePhone() string { return fmt.Sprintf("+2376%08d", rand.IntN(100_000_000)) }

func TestANewGoogleAccountCompletesItsProfileThenSignsIn(t *testing.T) {
	identities := fakeIdentities{}
	router := newSocialRouter(t, identities)
	email := uniqueEmail()
	identities["google-1"] = googleAccount(email)

	// the first time: no account yet
	first := send(t, router, http.MethodPost, "", "/v1/users/social", map[string]string{"firebase_token": "google-1"})
	require.Equal(t, http.StatusNotFound, first.Code, first.Body.String())
	require.Equal(t, "NO_ACCOUNT", errorCode(t, first))

	phone := uniquePhone()
	created := send(t, router, http.MethodPost, "", "/v1/users/social/register", map[string]string{
		"firebase_token": "google-1", "first_name": "Paul", "last_name": "Ngono", "phone_number": phone,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	account := decode[BaseUserResponse](t, created)
	t.Cleanup(func() { _ = testStore.DeleteUser(context.Background(), account.User.ID) })
	require.NotNil(t, account.AccessToken)
	require.Equal(t, email, account.User.Email)
	require.Equal(t, phone, account.User.PhoneNumber)
	require.False(t, account.User.HasPassword)
	require.NotEmpty(t, account.User.Username)

	// then: signed in straight away
	again := send(t, router, http.MethodPost, "", "/v1/users/social", map[string]string{"firebase_token": "google-1"})
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())
	require.Equal(t, account.User.ID, decode[BaseUserResponse](t, again).User.ID)

	// sent twice: the same account
	twice := send(t, router, http.MethodPost, "", "/v1/users/social/register", map[string]string{
		"firebase_token": "google-1", "first_name": "Paul", "phone_number": phone,
	})
	require.Equal(t, http.StatusOK, twice.Code, twice.Body.String())

	// no password to type: it chooses one without the current one
	chosen := send(t, router, http.MethodPut, *account.AccessToken, "/v1/users/me/password", map[string]string{"password": "Choisi123"})
	require.Equal(t, http.StatusOK, chosen.Code, chosen.Body.String())
	require.True(t, decode[BaseUserResponse](t, chosen).User.HasPassword)
	require.Equal(t, http.StatusOK, loginCode(t, router, phone, "Choisi123"))
}

func TestAGoogleAccountFindsTheAccountOfItsEmail(t *testing.T) {
	identities := fakeIdentities{}
	router := newSocialRouter(t, identities)
	email := uniqueEmail()
	boris := registerMember(t, router, map[string]string{"email": email})

	identities["google-same-email"] = googleAccount(strings.ToUpper(email))
	signed := send(t, router, http.MethodPost, "", "/v1/users/social", map[string]string{"firebase_token": "google-same-email"})
	require.Equal(t, http.StatusOK, signed.Code, signed.Body.String())
	require.Equal(t, boris.ID, decode[BaseUserResponse](t, signed).User.ID)

	// an email the provider didn't check links nothing
	unchecked := googleAccount(email)
	unchecked.EmailVerified = false
	identities["google-unchecked"] = unchecked
	refused := send(t, router, http.MethodPost, "", "/v1/users/social", map[string]string{"firebase_token": "google-unchecked"})
	require.Equal(t, http.StatusNotFound, refused.Code)
}

func TestAnAccountWithAPasswordIsLinkedToApple(t *testing.T) {
	identities := fakeIdentities{}
	router := newSocialRouter(t, identities)
	boris := registerMember(t, router, nil)
	apple := googleAccount("")
	apple.Provider = socialauth.Apple
	identities["apple-1"] = apple

	// its number is taken: the app asks for the password
	taken := send(t, router, http.MethodPost, "", "/v1/users/social/register", map[string]string{
		"firebase_token": "apple-1", "first_name": "Boris", "phone_number": boris.PhoneNumber,
	})
	require.Equal(t, http.StatusConflict, taken.Code, taken.Body.String())
	require.Equal(t, "PHONE_TAKEN", errorCode(t, taken))

	wrong := send(t, router, http.MethodPost, "", "/v1/users/social/link", map[string]string{
		"firebase_token": "apple-1", "phone_number": boris.PhoneNumber, "password": "pas-le-bon",
	})
	require.Equal(t, http.StatusUnauthorized, wrong.Code)

	linked := send(t, router, http.MethodPost, "", "/v1/users/social/link", map[string]string{
		"firebase_token": "apple-1", "phone_number": boris.PhoneNumber, "password": "Secret123",
	})
	require.Equal(t, http.StatusOK, linked.Code, linked.Body.String())

	signed := send(t, router, http.MethodPost, "", "/v1/users/social", map[string]string{"firebase_token": "apple-1"})
	require.Equal(t, http.StatusOK, signed.Code)
	require.Equal(t, boris.ID, decode[BaseUserResponse](t, signed).User.ID)

	// linked to Boris: nobody else links it
	paul := registerMember(t, router, nil)
	other := send(t, router, http.MethodPost, "", "/v1/users/social/link", map[string]string{
		"firebase_token": "apple-1", "phone_number": paul.PhoneNumber, "password": "Secret123",
	})
	require.Equal(t, http.StatusConflict, other.Code)
	require.Equal(t, "IDENTITY_TAKEN", errorCode(t, other))
}

func TestAnAccountWithoutPasswordIsDeletedWithItsGoogleAccount(t *testing.T) {
	identities := fakeIdentities{}
	router := newSocialRouter(t, identities)
	identities["google-del"] = googleAccount(uniqueEmail())
	identities["google-other"] = googleAccount(uniqueEmail())
	created := send(t, router, http.MethodPost, "", "/v1/users/social/register", map[string]string{
		"firebase_token": "google-del", "first_name": "Paul", "phone_number": uniquePhone(),
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	account := decode[BaseUserResponse](t, created)
	t.Cleanup(func() { _ = testStore.DeleteUser(context.Background(), account.User.ID) })

	// another Google account doesn't prove it
	other := send(t, router, http.MethodDelete, *account.AccessToken, "/v1/users/me", map[string]string{"firebase_token": "google-other"})
	require.Equal(t, http.StatusUnauthorized, other.Code)

	deleted := send(t, router, http.MethodDelete, *account.AccessToken, "/v1/users/me", map[string]string{"firebase_token": "google-del"})
	require.Equal(t, http.StatusNoContent, deleted.Code, deleted.Body.String())
	_, err := testStore.GetUserById(context.Background(), account.User.ID)
	require.Error(t, err)
}

func TestTheSocialSignInWithoutFirebase(t *testing.T) {
	router := newTestRouter(t)
	refused := send(t, router, http.MethodPost, "", "/v1/users/social", map[string]string{"firebase_token": "x"})
	require.Equal(t, http.StatusServiceUnavailable, refused.Code)
	require.Equal(t, "SOCIAL_SIGN_IN_UNAVAILABLE", errorCode(t, refused))
}

func TestAnAdminGivesATemporaryPassword(t *testing.T) {
	router := newTestRouter(t)
	admin := registerMember(t, router, nil)
	setRole(t, admin.ID, roleAdmin)
	member := registerMember(t, router, nil)

	given := send(t, router, http.MethodPost, admin.Token, fmt.Sprintf("/v1/admin/members/%d/password", member.ID), nil)
	require.Equal(t, http.StatusOK, given.Code, given.Body.String())
	temporary := decode[temporaryPasswordResponse](t, given).TemporaryPassword
	require.Regexp(t, `^[a-z2-9]{4}-[a-z2-9]{4}$`, temporary)

	// the old password no longer opens it; the temporary one asks for a new one
	require.Equal(t, http.StatusUnauthorized, loginCode(t, router, member.PhoneNumber, "Secret123"))
	login := send(t, router, http.MethodPost, "", "/v1/users/login",
		map[string]string{"phone_number": member.PhoneNumber, "password": temporary})
	require.Equal(t, http.StatusOK, login.Code)
	signed := decode[BaseUserResponse](t, login)
	require.True(t, signed.User.MustChangePassword)

	// the current password is needed
	wrong := send(t, router, http.MethodPut, *signed.AccessToken, "/v1/users/me/password",
		map[string]string{"current_password": "nope", "password": "MonNouveau1"})
	require.Equal(t, http.StatusUnauthorized, wrong.Code)
	changed := send(t, router, http.MethodPut, *signed.AccessToken, "/v1/users/me/password",
		map[string]string{"current_password": temporary, "password": "MonNouveau1"})
	require.Equal(t, http.StatusOK, changed.Code, changed.Body.String())
	require.False(t, decode[BaseUserResponse](t, changed).User.MustChangePassword)
	require.Equal(t, http.StatusOK, loginCode(t, router, member.PhoneNumber, "MonNouveau1"))
}

func TestOnlyASuperadminResetsTheTeamsPasswords(t *testing.T) {
	router := newTestRouter(t)
	admin := registerMember(t, router, nil)
	setRole(t, admin.ID, roleAdmin)
	other := registerMember(t, router, nil)
	setRole(t, other.ID, roleAdmin)
	member := registerMember(t, router, nil)

	path := func(id int64) string { return fmt.Sprintf("/v1/admin/members/%d/password", id) }
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPost, admin.Token, path(other.ID), nil).Code)
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPost, admin.Token, path(admin.ID), nil).Code)
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPost, member.Token, path(admin.ID), nil).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPost, admin.Token, path(1<<40), nil).Code)

	setRole(t, admin.ID, roleSuperadmin)
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPost, admin.Token, path(other.ID), nil).Code)
}
