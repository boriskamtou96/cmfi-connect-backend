package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/stretchr/testify/require"
)

func newTestRouter(t *testing.T) http.Handler {
	server, err := New(testStore, &utils.Config{Token: utils.TokenConfig{SecretKey: strings.Repeat("k", 32), Duration: "15m"}})
	require.NoError(t, err)
	return server.SetupRouter()
}

func send(t *testing.T, router http.Handler, method, token, path string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decode[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	var body struct {
		Content T `json:"content"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body), response.Body.String())
	return body.Content
}

func errorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	var body ErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return body.Code
}

// member is an account made through the API for a test, and signed in.
type member struct {
	userResponse
	Token string
}

// uniqueName gives names nobody has: the pseudos made from them are free.
func uniqueName() string {
	letters := "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 8)
	for i := range b {
		b[i] = letters[rand.IntN(len(letters))]
	}
	return "T" + string(b)
}

func registerMember(t *testing.T, router http.Handler, fields map[string]string) member {
	phone := fmt.Sprintf("+2376%08d", rand.IntN(100_000_000))
	body := map[string]string{
		"first_name":    uniqueName(),
		"last_name":     uniqueName(),
		"phone_number":  phone,
		"hash_password": "Secret123",
	}
	for key, value := range fields {
		body[key] = value
	}
	response := send(t, router, http.MethodPost, "", "/v1/users/register", body)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())

	login := send(t, router, http.MethodPost, "", "/v1/users/login",
		map[string]string{"phone_number": phone, "password": "Secret123"})
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	account := decode[BaseUserResponse](t, login)
	require.NotNil(t, account.AccessToken)

	t.Cleanup(func() { _ = testStore.DeleteUser(context.Background(), account.User.ID) })
	return member{userResponse: account.User, Token: *account.AccessToken}
}

func setRole(t *testing.T, id int64, role string) {
	_, err := testStore.UpdateUserRole(context.Background(), db.UpdateUserRoleParams{ID: id, Role: role})
	require.NoError(t, err)
}

func TestRegisterWithAChosenUsername(t *testing.T) {
	router := newTestRouter(t)
	username := strings.ToLower("my." + uniqueName())

	boris := registerMember(t, router, map[string]string{"username": "@" + strings.ToUpper(username)})

	require.Equal(t, username, boris.Username)
	require.Equal(t, "user", boris.Role)
	require.NotZero(t, boris.ID)
}

func TestRegisterWithoutUsernameMakesOneFromTheName(t *testing.T) {
	router := newTestRouter(t)
	first, last := uniqueName(), uniqueName()

	boris := registerMember(t, router, map[string]string{"first_name": first, "last_name": last})

	require.Equal(t, strings.ToLower(first+"."+last), boris.Username)
}

func TestRegisterRefusesAUsedInvalidOrReservedUsername(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)

	for username, want := range map[string]struct {
		status int
		code   string
	}{
		boris.Username: {http.StatusConflict, "USERNAME_TAKEN"},
		"a b":          {http.StatusBadRequest, "USERNAME_INVALID"},
		"admin":        {http.StatusBadRequest, "USERNAME_RESERVED"},
	} {
		response := send(t, router, http.MethodPost, "", "/v1/users/register", map[string]string{
			"first_name": "Jean", "last_name": "Dupont", "hash_password": "Secret123",
			"phone_number": fmt.Sprintf("+2376%08d", rand.IntN(100_000_000)), "username": username,
		})
		require.Equal(t, want.status, response.Code, username)
		require.Equal(t, want.code, errorCode(t, response), username)
	}
}

func TestUsernameAvailability(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)

	taken := get(t, router, "", "/v1/usernames/availability?username="+boris.Username+
		"&first_name="+boris.FirstName+"&last_name="+boris.LastName, nil)
	require.Equal(t, http.StatusOK, taken.Code)
	answer := decode[usernameAvailabilityResponse](t, taken)
	require.False(t, answer.Available)
	require.EqualValues(t, "taken", answer.Problem)
	// the first suggestion is the pseudo already given: the others are offered
	require.Len(t, answer.Suggestions, 3)
	require.NotContains(t, answer.Suggestions, boris.Username)

	free := decode[usernameAvailabilityResponse](t,
		get(t, router, "", "/v1/usernames/availability?username="+strings.ToLower(uniqueName()), nil))
	require.True(t, free.Available)

	reserved := decode[usernameAvailabilityResponse](t,
		get(t, router, "", "/v1/usernames/availability?username=Support", nil))
	require.Equal(t, "support", reserved.Username)
	require.EqualValues(t, "reserved", reserved.Problem)
}

func TestAMemberIsFoundByUsernameWithoutContact(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)
	marc := registerMember(t, router, nil)

	response := get(t, router, boris.Token, "/v1/users/by-username/@"+strings.ToUpper(marc.Username), nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.NotContains(t, response.Body.String(), marc.PhoneNumber)
	card := decode[memberCard](t, response)
	require.Equal(t, marc.Username, card.Username)
	require.Equal(t, marc.FirstName, card.FirstName)

	require.Equal(t, http.StatusNotFound, get(t, router, boris.Token, "/v1/users/by-username/nobody.here", nil).Code)
	require.Equal(t, http.StatusUnauthorized, get(t, router, "", "/v1/users/by-username/"+marc.Username, nil).Code)
}

func TestAccountsAreOnlyReadAndChangedByTheirOwner(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)
	marc := registerMember(t, router, nil)
	marcPath := fmt.Sprintf("/v1/users/%d/", marc.ID)

	// signed out: nothing
	require.Equal(t, http.StatusUnauthorized, send(t, router, http.MethodGet, "", marcPath, nil).Code)
	require.Equal(t, http.StatusUnauthorized, send(t, router, http.MethodDelete, "", marcPath, nil).Code)
	require.Equal(t, http.StatusUnauthorized, send(t, router, http.MethodPut, "", marcPath, map[string]string{"first_name": "X"}).Code)

	// another member: refused
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodGet, boris.Token, marcPath, nil).Code)
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPut, boris.Token, marcPath, map[string]string{"first_name": "X"}).Code)
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodDelete, boris.Token, marcPath, nil).Code)

	// the owner
	require.Equal(t, http.StatusOK, send(t, router, http.MethodGet, marc.Token, marcPath, nil).Code)
	me := decode[BaseUserResponse](t, get(t, router, marc.Token, "/v1/users/me", nil))
	require.Equal(t, marc.Username, me.User.Username)

	// an admin reads any account; deleting one takes a superadmin
	setRole(t, boris.ID, "admin")
	require.Equal(t, http.StatusOK, send(t, router, http.MethodGet, boris.Token, marcPath, nil).Code)
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodDelete, boris.Token, marcPath, nil).Code)
	setRole(t, boris.ID, "superadmin")
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, boris.Token, marcPath, nil).Code)
}

func TestAMemberChangesTheirUsername(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)
	marc := registerMember(t, router, nil)
	username := strings.ToLower("new." + uniqueName())

	response := send(t, router, http.MethodPut, boris.Token, "/v1/users/me/username", map[string]string{"username": username})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, username, decode[BaseUserResponse](t, response).User.Username)

	taken := send(t, router, http.MethodPut, boris.Token, "/v1/users/me/username", map[string]string{"username": marc.Username})
	require.Equal(t, http.StatusConflict, taken.Code)
	require.Equal(t, "USERNAME_TAKEN", errorCode(t, taken))
}

func TestOnlyASuperadminGivesRoles(t *testing.T) {
	router := newTestRouter(t)
	boss := registerMember(t, router, nil)
	boris := registerMember(t, router, nil)
	marc := registerMember(t, router, nil)
	rolePath := "/v1/admin/members/" + marc.Username + "/role"

	// a member: no admin routes
	require.Equal(t, http.StatusForbidden, get(t, router, boris.Token, "/v1/admin/staff", nil).Code)
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPut, boris.Token, rolePath, map[string]string{"role": "admin"}).Code)

	setRole(t, boss.ID, "superadmin")
	response := send(t, router, http.MethodPut, boss.Token, "/v1/admin/members/"+boris.Username+"/role", map[string]string{"role": "admin"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "admin", decode[staffMember](t, response).Role)

	// an admin sees the staff, but gives no role
	staff := decode[[]staffMember](t, get(t, router, boris.Token, "/v1/admin/staff", nil))
	usernames := map[string]string{}
	for _, person := range staff {
		usernames[person.Username] = person.Role
	}
	require.Equal(t, "admin", usernames[boris.Username])
	require.Equal(t, "superadmin", usernames[boss.Username])
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPut, boris.Token, rolePath, map[string]string{"role": "admin"}).Code)

	// a superadmin keeps their own role, and only real roles are given
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPut, boss.Token, "/v1/admin/members/"+boss.Username+"/role", map[string]string{"role": "user"}).Code)
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPut, boss.Token, rolePath, map[string]string{"role": "pope"}).Code)

	// taken back: the rights go at once, without signing in again
	require.Equal(t, http.StatusOK, send(t, router, http.MethodPut, boss.Token, "/v1/admin/members/"+boris.Username+"/role", map[string]string{"role": "user"}).Code)
	require.Equal(t, http.StatusForbidden, get(t, router, boris.Token, "/v1/admin/staff", nil).Code)
}
