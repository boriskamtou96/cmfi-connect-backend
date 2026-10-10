package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func deleteAccount(t *testing.T, router http.Handler, token, password string) *httptest.ResponseRecorder {
	return send(t, router, http.MethodDelete, token, "/v1/users/me", map[string]string{"password": password})
}

func TestAMemberDeletesTheirAccountAndEverythingTheirs(t *testing.T) {
	router := newTestRouter(t)
	ctx := context.Background()
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)

	// a personal item and a day that uses it
	created := send(t, router, http.MethodPost, boris.Token, "/v1/activities/",
		map[string]any{"code": "JEUNE", "label": "Jeûne", "tracks_duration": true})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	item := decode[activityTypeResponse](t, created)
	day := send(t, router, http.MethodPut, boris.Token, "/v1/reports/"+time.Now().Format("2006-01-02"),
		map[string]any{"entries": []map[string]any{{"activity_type_id": item.ID, "duration_minutes": 60}}})
	require.Equal(t, http.StatusOK, day.Code, day.Body.String())

	// an authority typed by hand, and Marie who asks Boris to accompany her
	byHand := send(t, router, http.MethodPost, boris.Token, "/v1/authorities/", map[string]any{
		"first_name": "Marc", "last_name": "Tientcheu", "phone_number": "+237690000001", "email": "", "is_disciple_maker": true,
	})
	require.Equal(t, http.StatusCreated, byHand.Code, byHand.Body.String())
	asked := send(t, router, http.MethodPost, marie.Token, "/v1/authorities/",
		map[string]any{"username": boris.Username, "is_disciple_maker": true})
	require.Equal(t, http.StatusCreated, asked.Code, asked.Body.String())

	// an event he publishes as an admin, and one he registers for
	setRole(t, boris.ID, "admin")
	published := createEvent(t, router, boris.Token, nil)
	other := createEvent(t, router, staffAdmin(t, router).Token, nil)
	registered := send(t, router, http.MethodPut, boris.Token, eventPath(other, "/registration"), map[string]any{"people": 2})
	require.Equal(t, http.StatusOK, registered.Code, registered.Body.String())

	before := send(t, router, http.MethodGet, marie.Token, "/v1/authorities/?page=1&size=10", nil)
	require.Contains(t, before.Body.String(), boris.Username)

	response := deleteAccount(t, router, boris.Token, "Secret123")
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())

	_, err := testStore.GetUserById(ctx, boris.ID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	// his token now leads nowhere
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodGet, boris.Token, "/v1/users/me", nil).Code)
	// Marie's request to him is gone
	marieAuthorities := send(t, router, http.MethodGet, marie.Token, "/v1/authorities/?page=1&size=10", nil)
	require.Equal(t, http.StatusOK, marieAuthorities.Code)
	require.NotContains(t, marieAuthorities.Body.String(), boris.Username)
	// his registration is gone, the event he published stays without him
	counts, err := testStore.CountEventRegistrations(ctx, []int64{other.ID})
	require.NoError(t, err)
	for _, count := range counts {
		require.Zero(t, count.People)
	}
	kept, err := testStore.GetEvent(ctx, published.ID)
	require.NoError(t, err)
	require.False(t, kept.CreatedBy.Valid)
}

func TestDeletingTheAccountNeedsThePassword(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)

	wrong := deleteAccount(t, router, boris.Token, "not-my-password")
	require.Equal(t, http.StatusUnauthorized, wrong.Code)
	require.Equal(t, "INVALID_CREDENTIALS", errorCode(t, wrong))

	missing := send(t, router, http.MethodDelete, boris.Token, "/v1/users/me", map[string]string{})
	require.Equal(t, http.StatusBadRequest, missing.Code)

	_, err := testStore.GetUserById(context.Background(), boris.ID)
	require.NoError(t, err)
}

func TestTheLastSuperadminKeepsTheirAccount(t *testing.T) {
	router := newTestRouter(t)
	ctx := context.Background()
	boris := registerMember(t, router, nil)
	setRole(t, boris.ID, "superadmin")
	count, err := testStore.CountSuperadmins(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "the test database must have no other superadmin")

	alone := deleteAccount(t, router, boris.Token, "Secret123")
	require.Equal(t, http.StatusConflict, alone.Code)
	require.Equal(t, "LAST_SUPERADMIN", errorCode(t, alone))

	// with a second superadmin, he can go
	marie := registerMember(t, router, nil)
	setRole(t, marie.ID, "superadmin")
	require.Equal(t, http.StatusNoContent, deleteAccount(t, router, boris.Token, "Secret123").Code)
}
