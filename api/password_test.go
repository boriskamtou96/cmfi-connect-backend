package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/phoneauth"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/stretchr/testify/require"
)

// fakePhones: the token → the number Firebase would have verified.
type fakePhones map[string]string

func (f fakePhones) VerifiedPhone(_ context.Context, idToken string) (string, error) {
	if phone, ok := f[idToken]; ok {
		return phone, nil
	}
	return "", phoneauth.ErrInvalid
}

func newResetRouter(t *testing.T, phones phoneauth.Verifier) http.Handler {
	server, err := New(testStore, &utils.Config{Token: utils.TokenConfig{SecretKey: strings.Repeat("k", 32), Duration: "15m"}})
	require.NoError(t, err)
	server.phones = phones
	return server.SetupRouter()
}

func loginCode(t *testing.T, router http.Handler, phone, password string) int {
	return send(t, router, http.MethodPost, "", "/v1/users/login",
		map[string]string{"phone_number": phone, "password": password}).Code
}

func TestAForgottenPasswordIsResetWithTheSMSCode(t *testing.T) {
	phones := fakePhones{}
	router := newResetRouter(t, phones)
	boris := registerMember(t, router, nil)
	phones["sms-ok"] = boris.PhoneNumber

	reset := send(t, router, http.MethodPost, "", "/v1/users/password/reset",
		map[string]string{"firebase_token": "sms-ok", "password": "Nouveau456"})
	require.Equal(t, http.StatusNoContent, reset.Code, reset.Body.String())

	require.Equal(t, http.StatusOK, loginCode(t, router, boris.PhoneNumber, "Nouveau456"))
	require.Equal(t, http.StatusUnauthorized, loginCode(t, router, boris.PhoneNumber, "Secret123"))
}

func TestAPasswordResetNeedsAVerifiedPhoneOfAnAccount(t *testing.T) {
	phones := fakePhones{"sms-unknown": "+237600000000"}
	router := newResetRouter(t, phones)
	boris := registerMember(t, router, nil)

	for _, tc := range []struct {
		body map[string]string
		code int
	}{
		// a code Firebase didn't verify
		{map[string]string{"firebase_token": "made-up", "password": "Nouveau456"}, http.StatusUnauthorized},
		// a number without account
		{map[string]string{"firebase_token": "sms-unknown", "password": "Nouveau456"}, http.StatusNotFound},
		// too short, or nothing
		{map[string]string{"firebase_token": "sms-unknown", "password": "abc"}, http.StatusBadRequest},
		{map[string]string{"password": "Nouveau456"}, http.StatusBadRequest},
	} {
		response := send(t, router, http.MethodPost, "", "/v1/users/password/reset", tc.body)
		require.Equal(t, tc.code, response.Code, tc.body)
	}
	require.Equal(t, http.StatusOK, loginCode(t, router, boris.PhoneNumber, "Secret123"))
}

func TestWithoutFirebaseKeyThePasswordCantBeReset(t *testing.T) {
	router := newTestRouter(t)

	response := send(t, router, http.MethodPost, "", "/v1/users/password/reset",
		map[string]string{"firebase_token": "anything", "password": "Nouveau456"})
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Contains(t, response.Body.String(), "PASSWORD_RESET_UNAVAILABLE")
}
