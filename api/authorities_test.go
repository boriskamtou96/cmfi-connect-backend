package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func listMyAuthorities(t *testing.T, router http.Handler, token string) []authorityResponse {
	response := get(t, router, token, "/v1/authorities/?page=1&size=10", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[[]authorityResponse](t, response)
}

func TestAnAuthorityFoundByUsernameWaitsForTheirAnswer(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)
	marc := registerMember(t, router, nil)

	created := send(t, router, http.MethodPost, boris.Token, "/v1/authorities/",
		map[string]any{"username": "@" + marc.Username, "is_disciple_maker": true})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	authority := decode[authorityResponse](t, created)
	require.Equal(t, "pending", authority.Status)
	require.Equal(t, marc.Username, *authority.Username)
	require.Equal(t, marc.FirstName, authority.FirstName)
	// no contact before they accept
	require.Nil(t, authority.PhoneNumber)

	// Marc sees who asks, and accepts
	requests := decode[[]authorityRequestResponse](t, get(t, router, marc.Token, "/v1/authorities/requests", nil))
	require.Len(t, requests, 1)
	require.Equal(t, boris.Username, requests[0].From.Username)
	require.True(t, requests[0].IsDiscipleMaker)

	accepted := send(t, router, http.MethodPost, marc.Token, fmt.Sprintf("/v1/authorities/requests/%d/accept", authority.ID), nil)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	require.Equal(t, "accepted", decode[authorityRequestResponse](t, accepted).Status)
	require.Empty(t, decode[[]authorityRequestResponse](t, get(t, router, marc.Token, "/v1/authorities/requests", nil)))

	// now Boris has his contact
	mine := listMyAuthorities(t, router, boris.Token)
	require.Len(t, mine, 1)
	require.Equal(t, "accepted", mine[0].Status)
	require.Equal(t, marc.PhoneNumber, *mine[0].PhoneNumber)

	// an answer is given once
	again := send(t, router, http.MethodPost, marc.Token, fmt.Sprintf("/v1/authorities/requests/%d/decline", authority.ID), nil)
	require.Equal(t, http.StatusNotFound, again.Code)
}

func TestADeclinedRequestFreesTheDiscipleMakerPlace(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)
	marc := registerMember(t, router, nil)
	paul := registerMember(t, router, nil)

	asked := decode[authorityResponse](t, send(t, router, http.MethodPost, boris.Token, "/v1/authorities/",
		map[string]any{"username": marc.Username, "is_disciple_maker": true}))

	// one disciple maker at a time
	second := send(t, router, http.MethodPost, boris.Token, "/v1/authorities/",
		map[string]any{"username": paul.Username, "is_disciple_maker": true})
	require.Equal(t, http.StatusConflict, second.Code)
	require.Equal(t, "DISCIPLE_MAKER_EXISTS", errorCode(t, second))

	// only the member asked can answer
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPost, paul.Token,
		fmt.Sprintf("/v1/authorities/requests/%d/accept", asked.ID), nil).Code)

	declined := send(t, router, http.MethodPost, marc.Token, fmt.Sprintf("/v1/authorities/requests/%d/decline", asked.ID), nil)
	require.Equal(t, http.StatusOK, declined.Code)
	require.Equal(t, "declined", listMyAuthorities(t, router, boris.Token)[0].Status)

	require.Equal(t, http.StatusCreated, send(t, router, http.MethodPost, boris.Token, "/v1/authorities/",
		map[string]any{"username": paul.Username, "is_disciple_maker": true}).Code)
}

func TestAuthorityByUsernameMistakes(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)
	marc := registerMember(t, router, nil)

	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPost, boris.Token, "/v1/authorities/",
		map[string]any{"username": "nobody.here"}).Code)
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPost, boris.Token, "/v1/authorities/",
		map[string]any{"username": boris.Username}).Code)

	require.Equal(t, http.StatusCreated, send(t, router, http.MethodPost, boris.Token, "/v1/authorities/",
		map[string]any{"username": marc.Username}).Code)
	twice := send(t, router, http.MethodPost, boris.Token, "/v1/authorities/", map[string]any{"username": marc.Username})
	require.Equal(t, http.StatusConflict, twice.Code)
	require.Equal(t, "AUTHORITY_EXISTS", errorCode(t, twice))
}

func TestAuthoritiesTypedByHandAndOwnedByOne(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)
	marc := registerMember(t, router, nil)

	// by hand: accepted at once; the same e-mail may be used by two members
	for _, owner := range []member{boris, marc} {
		response := send(t, router, http.MethodPost, owner.Token, "/v1/authorities/", map[string]any{
			"first_name": "Clarisse", "last_name": "Nguele", "phone_number": "+237677554433", "email": "clarisse@example.com",
		})
		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		require.Equal(t, "accepted", decode[authorityResponse](t, response).Status)
	}
	require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPost, boris.Token, "/v1/authorities/",
		map[string]any{"first_name": "Sans téléphone"}).Code)

	// each member sees only their own, and can't read nor delete another's
	mine := listMyAuthorities(t, router, boris.Token)
	require.Len(t, mine, 1)
	theirs := listMyAuthorities(t, router, marc.Token)[0]
	path := fmt.Sprintf("/v1/authorities/%d", theirs.ID)
	require.Equal(t, http.StatusNotFound, get(t, router, boris.Token, path, nil).Code)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodDelete, boris.Token, path, nil).Code)

	got := get(t, router, marc.Token, path, nil)
	require.Equal(t, http.StatusOK, got.Code)
	require.Equal(t, "+237677554433", *decode[authorityResponse](t, got).PhoneNumber)
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, marc.Token, path, nil).Code)
}
