package api

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// An account made before the profiles (no profile yet) creates its own:
// the e-mail goes to the e-mail, the last name stays the last name.
func TestCreatingAProfileKeepsTheEmailAndTheLastName(t *testing.T) {
	router := newTestRouter(t)
	phone := fmt.Sprintf("+2376%08d", rand.IntN(100_000_000))
	hash, err := bcrypt.GenerateFromPassword([]byte("Secret123"), bcrypt.MinCost)
	require.NoError(t, err)
	user, err := testStore.RegisterUser(context.Background(), db.RegisterUserParams{
		Username:     strings.ToLower("profile." + uniqueName()),
		FirstName:    "Esther",
		PhoneNumber:  phone,
		HashPassword: hash,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = testStore.DeleteUser(context.Background(), user.ID) })

	login := send(t, router, http.MethodPost, "", "/v1/users/login",
		map[string]string{"phone_number": phone, "password": "Secret123"})
	require.Equal(t, http.StatusOK, login.Code, login.Body.String())
	token := *decode[BaseUserResponse](t, login).AccessToken

	created := send(t, router, http.MethodPost, token, "/v1/profiles/", map[string]any{
		"first_name":   "Esther",
		"last_name":    "Mbarga",
		"phone_number": phone,
		"email":        "esther@example.com",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	profile := decode[profileResponse](t, created)
	require.Equal(t, "Mbarga", profile.LastName)
	require.Equal(t, "esther@example.com", profile.Email)
}
