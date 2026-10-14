package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func inviteTo(t *testing.T, router http.Handler, inviter member, topic importunityDetailResponse, invited member) int64 {
	response := send(t, router, http.MethodPost, inviter.Token, importunityPath(topic, "/invitations"),
		map[string]any{"usernames": []string{"@" + invited.Username}})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[invitationsResponse](t, response).Invited
}

func TestABlockedMemberCanNoLongerInvite(t *testing.T) {
	router, sender := newPushRouter(t, "")
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)
	addPhone(t, router, marie, "phone-"+marie.Username)
	topic := createImportunity(t, router, boris.Token, 100)
	require.Equal(t, int64(1), inviteTo(t, router, boris, topic, marie))
	require.Len(t, sender.messages, 1)

	block := "/v1/members/" + boris.Username + "/block"
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodPost, marie.Token, block, nil).Code)
	// twice is fine
	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodPost, marie.Token, block, nil).Code)

	// the invitation went; a new one is ignored, without push, and Boris
	// isn't told
	require.Empty(t, decode[[]importunityResponse](t, get(t, router, marie.Token, "/v1/prayer/importunities", nil)))
	other := createImportunity(t, router, boris.Token, 50)
	require.Equal(t, int64(0), inviteTo(t, router, boris, other, marie))
	require.Len(t, sender.messages, 1)

	blocked := decode[[]memberCard](t, get(t, router, marie.Token, "/v1/members/blocked", nil))
	require.Len(t, blocked, 1)
	require.Equal(t, boris.Username, blocked[0].Username)

	require.Equal(t, http.StatusNoContent, send(t, router, http.MethodDelete, marie.Token, block, nil).Code)
	require.Empty(t, decode[[]memberCard](t, get(t, router, marie.Token, "/v1/members/blocked", nil)))
	require.Equal(t, int64(1), inviteTo(t, router, boris, other, marie))

	// not oneself, not someone unknown
	require.Equal(t, http.StatusBadRequest,
		send(t, router, http.MethodPost, marie.Token, "/v1/members/"+marie.Username+"/block", nil).Code)
	require.Equal(t, http.StatusNotFound,
		send(t, router, http.MethodPost, marie.Token, "/v1/members/personne.inconnue/block", nil).Code)
}

type reportsPage struct {
	Content []reportResponse `json:"content"`
	Meta    MetaResponse     `json:"meta"`
}

func listedReports(t *testing.T, router http.Handler, token, query string) reportsPage {
	response := send(t, router, http.MethodGet, token, "/v1/admin/reports?"+query, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page reportsPage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	return page
}

func reportIn(page reportsPage, id int64) *reportResponse {
	for i := range page.Content {
		if page.Content[i].ID == id {
			return &page.Content[i]
		}
	}
	return nil
}

func TestAReportReachesTheAdmins(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	boris := registerMember(t, router, nil)
	marie := registerMember(t, router, nil)
	topic := createImportunity(t, router, boris.Token, 100)

	created := send(t, router, http.MethodPost, marie.Token, "/v1/reports", map[string]any{
		"username": "@" + boris.Username, "importunity_id": topic.ID, "reason": "offensive", "comment": "  Pas correct  ",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	id := int64(decode[map[string]float64](t, created)["id"])

	for _, body := range []map[string]any{
		{"username": boris.Username, "reason": "boring"},
		{"username": marie.Username, "reason": "spam"},
	} {
		require.Equal(t, http.StatusBadRequest, send(t, router, http.MethodPost, marie.Token, "/v1/reports", body).Code, body)
	}
	require.Equal(t, http.StatusNotFound, send(t, router, http.MethodPost, marie.Token, "/v1/reports",
		map[string]any{"username": "personne.inconnue", "reason": "spam"}).Code)

	// members don't see the reports
	require.Equal(t, http.StatusForbidden, send(t, router, http.MethodGet, marie.Token, "/v1/admin/reports", nil).Code)

	report := reportIn(listedReports(t, router, admin.Token, "status=open&size=100"), id)
	require.NotNil(t, report)
	require.Equal(t, "offensive", report.Reason)
	require.Equal(t, "Pas correct", report.Comment)
	require.Equal(t, marie.Username, report.Reporter.Username)
	require.Equal(t, boris.Username, report.Reported.Username)
	require.Equal(t, "user", report.Reported.Role)
	require.Equal(t, topic.Title, report.Importunity.Title)
	require.Equal(t, int32(1), report.OpenReportsAboutReported)
	require.Nil(t, report.ResolvedAt)

	resolve := fmt.Sprintf("/v1/admin/reports/%d/resolve", id)
	resolved := send(t, router, http.MethodPut, admin.Token, resolve, nil)
	require.Equal(t, http.StatusOK, resolved.Code, resolved.Body.String())
	require.Equal(t, "resolved", decode[reportResponse](t, resolved).Status)
	require.Nil(t, reportIn(listedReports(t, router, admin.Token, "status=open&size=100"), id))
	require.NotNil(t, reportIn(listedReports(t, router, admin.Token, "status=resolved&size=100"), id))
	require.Equal(t, http.StatusNotFound,
		send(t, router, http.MethodPut, admin.Token, "/v1/admin/reports/999999999/resolve", nil).Code)
	require.Equal(t, http.StatusBadRequest,
		send(t, router, http.MethodGet, admin.Token, "/v1/admin/reports?status=maybe", nil).Code)
}
