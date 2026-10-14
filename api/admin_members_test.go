package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func listedMembers(t *testing.T, router http.Handler, token, query string) ([]adminMemberResponse, int64) {
	response := send(t, router, http.MethodGet, token, "/v1/admin/members?"+query, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page struct {
		Content []adminMemberResponse `json:"content"`
		Meta    MetaResponse          `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	return page.Content, page.Meta.Total
}

func TestTheBackOfficeFindsTheMembers(t *testing.T) {
	router := newTestRouter(t)
	admin := staffAdmin(t, router)
	family := uniqueName()
	paul := registerMember(t, router, map[string]string{"first_name": "Paul", "last_name": family})
	esther := registerMember(t, router, map[string]string{"first_name": "Esther", "last_name": family})
	livesIn(t, router, paul, "Yaoundé", "Mvolyé")
	livesIn(t, router, esther, "Douala", "Akwa")
	addPhone(t, router, paul, "phone-"+family)

	// a member can't look
	require.Equal(t, http.StatusForbidden,
		send(t, router, http.MethodGet, paul.Token, "/v1/admin/members", nil).Code)

	// by the name, the newest first
	found, total := listedMembers(t, router, admin.Token, "search="+family)
	require.Equal(t, int64(2), total)
	require.Equal(t, []string{esther.Username, paul.Username}, []string{found[0].Username, found[1].Username})

	// by the city without its accent, with the names the other way round
	found, _ = listedMembers(t, router, admin.Token, "city=yaounde&search="+strings.ToLower(family)+"+paul")
	require.Len(t, found, 1)
	require.Equal(t, paul.ID, found[0].ID)
	require.Equal(t, "CM", found[0].Country)
	require.Equal(t, "Mvolyé", found[0].Assembly)
	require.Equal(t, int32(1), found[0].Phones)
	require.NotNil(t, found[0].LastSeenAt)

	// by the digits of the phone, by the pseudo, by the role
	digits := paul.PhoneNumber[len(paul.PhoneNumber)-6:]
	found, _ = listedMembers(t, router, admin.Token, "search="+digits)
	require.Contains(t, ids(found), paul.ID)
	found, _ = listedMembers(t, router, admin.Token, "search=@"+esther.Username)
	require.Equal(t, []int64{esther.ID}, ids(found))
	found, _ = listedMembers(t, router, admin.Token, "role=admin&search="+family)
	require.Empty(t, found)

	// a page at a time
	found, total = listedMembers(t, router, admin.Token, "search="+family+"&page=2&size=1")
	require.Equal(t, int64(2), total)
	require.Equal(t, []int64{paul.ID}, ids(found))

	// the sheet of one member: no phone yet for Esther
	sheet := send(t, router, http.MethodGet, admin.Token, fmt.Sprintf("/v1/admin/members/%d", esther.ID), nil)
	require.Equal(t, http.StatusOK, sheet.Code, sheet.Body.String())
	member := decode[adminMemberResponse](t, sheet)
	require.Equal(t, "Esther", member.FirstName)
	require.Equal(t, "Douala", member.City)
	require.Equal(t, "user", member.Role)
	require.Nil(t, member.LastSeenAt)
	require.Equal(t, int32(0), member.Phones)
	require.Equal(t, http.StatusNotFound,
		send(t, router, http.MethodGet, admin.Token, "/v1/admin/members/999999999", nil).Code)
}

func ids(members []adminMemberResponse) []int64 {
	out := make([]int64, len(members))
	for i, member := range members {
		out[i] = member.ID
	}
	return out
}

func TestAnInvitationToAnImportunityReachesThePhoneInItsLanguage(t *testing.T) {
	router, sender := newPushRouter(t, "")
	boris := registerMember(t, router, map[string]string{"first_name": "Boris"})
	marie := registerMember(t, router, nil)
	response := send(t, router, http.MethodPut, marie.Token, "/v1/devices",
		map[string]string{"token": "marie-phone-" + marie.Username, "platform": "ios", "language": "en"})
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())

	topic := createImportunity(t, router, boris.Token, 100)
	invite := func() int64 {
		invited := send(t, router, http.MethodPost, boris.Token, importunityPath(topic, "/invitations"),
			map[string]any{"usernames": []string{"@" + marie.Username}})
		require.Equal(t, http.StatusOK, invited.Code, invited.Body.String())
		return decode[invitationsResponse](t, invited).Invited
	}
	require.Equal(t, int64(1), invite())

	require.Len(t, sender.messages, 1)
	message := sender.messages[0]
	require.Equal(t, "Boris invites you to pray", message.Title)
	require.Equal(t, topic.Title, message.Body)
	require.Equal(t, "importunity_invite", message.Data["type"])
	require.Equal(t, fmt.Sprint(topic.ID), message.Data["id"])
	require.Equal(t, []string{"marie-phone-" + marie.Username}, sender.tokens[0])

	// invited once: no second push
	require.Equal(t, int64(0), invite())
	require.Len(t, sender.messages, 1)
}

func TestTheInvitationTitleFallsBackToFrench(t *testing.T) {
	require.Equal(t, "Paul t'invite à prier", invitationTitle("fr", "Paul"))
	require.Equal(t, "Paul te invita a orar", invitationTitle("es-MX", "Paul"))
	require.Equal(t, "Paul t'invite à prier", invitationTitle("sw", "Paul"))
}
