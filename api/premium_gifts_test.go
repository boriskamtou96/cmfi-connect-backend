package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func (w paymentsWorld) offer(t *testing.T, token string, body map[string]any) premiumOfferResponse {
	t.Helper()
	response := send(t, w.router, http.MethodPost, token, "/v1/admin/premium/offers", body)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	w.server.gifting.Wait()
	return decode[premiumOfferResponse](t, response)
}

func TestAnAdminOffersPremiumToAMember(t *testing.T) {
	w := newPaymentsWorld(t)
	sender := &fakeSender{}
	w.server.push = sender
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	addPhone(t, w.router, m, "phone-"+uniqueName())

	offer := w.offer(t, admin.Token, map[string]any{"months": 2, "username": "@" + m.Username, "note": "Merci pour le camp"})
	require.Equal(t, int32(1), offer.MembersCount)
	require.Equal(t, int32(1), offer.Given)

	granted := w.premium.granted[fmt.Sprint(m.ID)]
	require.Len(t, granted, 1)
	require.WithinDuration(t, time.Now().AddDate(0, 2, 0), *granted[0], time.Minute)
	require.Len(t, sender.messages, 1)
	require.Equal(t, "Premium t'est offert", sender.messages[0].Title)
	require.Equal(t, "Profite de CMFI Connect Premium pendant 2 mois. Que Dieu te bénisse !", sender.messages[0].Body)
	require.Equal(t, "premium_gift", sender.messages[0].Data["type"])

	// the admins see it
	listed := decode[[]premiumOfferResponse](t, send(t, w.router, http.MethodGet, admin.Token, "/v1/admin/premium/offers", nil))
	require.Equal(t, offer.ID, listed[0].ID)
	require.Equal(t, admin.Username, listed[0].CreatedBy.Username)
}

func TestAGiftComesAfterThePremiumAlreadyBought(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	month := w.pay(t, m.Token, "/v1/premium/payments", map[string]any{"plan": "monthly", "phone_number": "+237670000031"})
	w.notify(t, "payment.completed", month.ID, map[string]any{"amount": 1500})

	w.offer(t, admin.Token, map[string]any{"months": 3, "username": m.Username})
	granted := w.premium.granted[fmt.Sprint(m.ID)]
	require.Len(t, granted, 2)
	require.WithinDuration(t, time.Now().AddDate(0, 1, 0).AddDate(0, 3, 0), *granted[1], time.Minute)
}

func TestPremiumIsOfferedToAGroupAndTakenBack(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	city := "Ville " + uniqueName()
	one := registerMember(t, w.router, nil)
	two := registerMember(t, w.router, nil)
	livesIn(t, w.router, one, city, "Centre")
	livesIn(t, w.router, two, city, "Nord")
	outside := registerMember(t, w.router, nil)

	offer := w.offer(t, admin.Token, map[string]any{"months": 1, "audience": map[string]string{"city": city}, "notify": false})
	require.Equal(t, int32(2), offer.MembersCount)
	require.Len(t, w.premium.granted[fmt.Sprint(one.ID)], 1)
	require.Len(t, w.premium.granted[fmt.Sprint(two.ID)], 1)
	require.Empty(t, w.premium.granted[fmt.Sprint(outside.ID)])

	// taken back: the promotional Premium goes, nothing else stands
	revoked := send(t, w.router, http.MethodDelete, admin.Token, fmt.Sprintf("/v1/admin/premium/offers/%d", offer.ID), nil)
	require.Equal(t, http.StatusOK, revoked.Code, revoked.Body.String())
	w.server.gifting.Wait()
	require.NotNil(t, decode[premiumOfferResponse](t, revoked).RevokedAt)
	require.Equal(t, 1, w.premium.revoked[fmt.Sprint(one.ID)])
	require.Len(t, w.premium.granted[fmt.Sprint(one.ID)], 1)
}

func TestOffersAreChecked(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)

	cases := []struct {
		body map[string]any
		code int
		want string
	}{
		{map[string]any{"months": 4, "username": m.Username}, http.StatusBadRequest, "BAD_REQUEST"},
		{map[string]any{"months": 1, "username": "nobody-" + uniqueName()}, http.StatusNotFound, "USERNAME_NOT_FOUND"},
		{map[string]any{"months": 1, "audience": map[string]string{"city": "Nowhere " + uniqueName()}}, http.StatusBadRequest, "NO_RECIPIENTS"},
	}
	for _, tc := range cases {
		response := send(t, w.router, http.MethodPost, admin.Token, "/v1/admin/premium/offers", tc.body)
		require.Equal(t, tc.code, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), tc.want)
	}
	// only the admins
	forbidden := send(t, w.router, http.MethodPost, m.Token, "/v1/admin/premium/offers", map[string]any{"months": 1, "username": m.Username})
	require.Equal(t, http.StatusForbidden, forbidden.Code)

	// without RevenueCat: said
	w.server.premium = nil
	disabled := send(t, w.router, http.MethodPost, admin.Token, "/v1/admin/premium/offers", map[string]any{"months": 1, "username": m.Username})
	require.Equal(t, http.StatusServiceUnavailable, disabled.Code)
	require.Contains(t, disabled.Body.String(), "PREMIUM_DISABLED")
}

func TestTheAdminsTakeBackTheOfferedPremiumOfAMember(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	user := fmt.Sprint(m.ID)
	month := w.pay(t, m.Token, "/v1/premium/payments", map[string]any{"plan": "monthly", "phone_number": "+237670000041"})
	w.notify(t, "payment.completed", month.ID, map[string]any{"amount": 1500})
	w.offer(t, admin.Token, map[string]any{"months": 2, "username": m.Username})
	path := "/v1/admin/premium/members/" + m.Username + "/revoke"

	// what was offered goes; the month paid is given again
	taken := send(t, w.router, http.MethodPost, admin.Token, path, map[string]any{})
	require.Equal(t, http.StatusOK, taken.Code, taken.Body.String())
	require.Equal(t, revokeMemberPremiumResponse{Username: m.Username, GiftsRevoked: 1}, decode[revokeMemberPremiumResponse](t, taken))
	require.Equal(t, 1, w.premium.revoked[user])
	granted := w.premium.granted[user]
	require.WithinDuration(t, time.Now().AddDate(0, 1, 0), *granted[len(granted)-1], time.Minute)

	// the paid one too: superadmins only (tests)
	forbidden := send(t, w.router, http.MethodPost, admin.Token, path, map[string]any{"include_paid": true})
	require.Equal(t, http.StatusForbidden, forbidden.Code)
	setRole(t, admin.ID, roleSuperadmin)
	all := send(t, w.router, http.MethodPost, admin.Token, path, map[string]any{"include_paid": true})
	require.Equal(t, http.StatusOK, all.Code, all.Body.String())
	require.Equal(t, int64(1), decode[revokeMemberPremiumResponse](t, all).PaidEnded)
	require.Equal(t, 2, w.premium.revoked[user])
	require.Len(t, w.premium.granted[user], len(granted))
	status := decodeRaw[rawContent](t, send(t, w.router, http.MethodGet, m.Token, "/v1/premium/status", nil))
	require.Contains(t, string(status.Content), `"until":null`)
}
