package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func listedNations(t *testing.T, router http.Handler, token string) []nationResponse {
	response := send(t, router, http.MethodGet, token, "/v1/ministry/nations", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[[]nationResponse](t, response)
}

// the numbers stay 1, 2, 3… without gap nor double
func requireNumbered(t *testing.T, nations []nationResponse) {
	for i, nation := range nations {
		require.Equal(t, int32(i+1), nation.Position, nation.Name)
	}
}

func TestTheNationsStartWithCameroonAndAreNumbered(t *testing.T) {
	router := newTestRouter(t)
	boris := registerMember(t, router, nil)

	nations := listedNations(t, router, boris.Token)
	require.GreaterOrEqual(t, len(nations), 154)
	requireNumbered(t, nations)
	require.Equal(t, "Le Cameroun", nations[0].Name)
	require.Equal(t, "CM", nations[0].CountryCode)
	require.Nil(t, nations[0].ArrivalYear)

	require.Equal(t, http.StatusUnauthorized, send(t, router, http.MethodGet, "", "/v1/ministry/nations", nil).Code)
}

func TestAnAdminAddsMovesAndRemovesANation(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)
	before := listedNations(t, router, boris.Token)

	// a member can't
	body := map[string]any{"name": "Le Vanuatu", "country_code": "vu", "latitude": -15.4, "longitude": 166.9}
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodPost, boris.Token, "/v1/admin/ministry/nations", body).Code)

	created := send(t, router, http.MethodPost, admin.Token, "/v1/admin/ministry/nations", body)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	vanuatu := decode[nationResponse](t, created)
	require.Equal(t, int32(len(before)+1), vanuatu.Position)
	require.Equal(t, "VU", vanuatu.CountryCode)
	path := fmt.Sprintf("/v1/admin/ministry/nations/%d", vanuatu.ID)
	t.Cleanup(func() { send(t, router, http.MethodDelete, admin.Token, path, nil) })

	// to the 2nd place, with its year: the others move down by one
	year := 2019
	moved := send(t, router, http.MethodPut, admin.Token, path, map[string]any{
		"name": "Le Vanuatu", "country_code": "VU", "latitude": -15.4, "longitude": 166.9,
		"arrival_year": year, "position": 2,
	})
	require.Equal(t, http.StatusOK, moved.Code, moved.Body.String())
	nations := listedNations(t, router, boris.Token)
	requireNumbered(t, nations)
	require.Equal(t, "Le Vanuatu", nations[1].Name)
	require.Equal(t, int32(year), *nations[1].ArrivalYear)
	require.Equal(t, before[1].Name, nations[2].Name)

	// without position, it keeps its place
	kept := send(t, router, http.MethodPut, admin.Token, path, map[string]any{
		"name": "Vanuatu", "country_code": "VU", "latitude": -15.4, "longitude": 166.9,
	})
	require.Equal(t, http.StatusOK, kept.Code, kept.Body.String())
	require.Equal(t, int32(2), decode[nationResponse](t, kept).Position)
	require.Nil(t, decode[nationResponse](t, kept).ArrivalYear)

	// removed: the list is as before
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, admin.Token, path, nil).Code)
	after := listedNations(t, router, boris.Token)
	requireNumbered(t, after)
	require.Equal(t, len(before), len(after))
	require.Equal(t, before[1].Name, after[1].Name)
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodDelete, admin.Token, path, nil).Code)
}

func TestANationNeedsANameAndAPlaceOnTheGlobe(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)

	for _, body := range []map[string]any{
		{"name": " ", "latitude": 1, "longitude": 1},
		{"name": "Nulle part", "longitude": 1},
		{"name": "Nulle part", "latitude": 95, "longitude": 1},
		{"name": "Nulle part", "country_code": "FRA", "latitude": 1, "longitude": 1},
		{"name": "Nulle part", "latitude": 1, "longitude": 1, "arrival_year": 1500},
	} {
		response := send(t, router, http.MethodPost, admin.Token, "/v1/admin/ministry/nations", body)
		require.Equal(t, http.StatusBadRequest, response.Code, body)
	}
	// on the equator and the meridian: 0 is a place
	created := send(t, router, http.MethodPost, admin.Token, "/v1/admin/ministry/nations",
		map[string]any{"name": "Golfe de Guinée", "latitude": 0, "longitude": 0})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	nation := decode[nationResponse](t, created)
	require.Equal(t, http.StatusNoContent,
		send(t, router, http.MethodDelete, admin.Token, fmt.Sprintf("/v1/admin/ministry/nations/%d", nation.ID), nil).Code)
}
