package api

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTheRenewalRequestsComeTheDayBeforeTheEndInDaytime(t *testing.T) {
	at := func(text string) time.Time {
		value, err := time.ParseInLocation("2006-01-02 15:04", text, renewalZone)
		require.NoError(t, err)
		return value
	}
	end := at("2026-12-16 12:21")
	// the first: the day before, at 10:00 in Yaoundé
	require.Equal(t, at("2026-12-15 10:00"), renewalTime(end, 0, sql.NullTime{}))
	// the next ones: a day after the last one
	require.Equal(t, at("2026-12-16 09:00"), renewalTime(end, 1, sql.NullTime{Time: at("2026-12-15 10:00"), Valid: true}))
	// never at night: in the morning
	require.Equal(t, at("2026-12-17 08:00"), renewalTime(end, 2, sql.NullTime{Time: at("2026-12-15 21:30"), Valid: true}))
	require.Equal(t, at("2026-12-17 08:00"), renewalTime(end, 2, sql.NullTime{Time: at("2026-12-16 06:10"), Valid: true}))

	require.Equal(t, "1 500 FCFA", moneyText(1500, "XAF"))
	require.Equal(t, "50 000 FCFA", moneyText(50000, "XAF"))
}

// renewalOf: the renewal the apps read for [token] (GET /v1/premium/status).
func (w paymentsWorld) renewalOf(t *testing.T, token string) *premiumRenewalResponse {
	t.Helper()
	response := send(t, w.router, http.MethodGet, token, "/v1/premium/status", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[struct {
		Renewal *premiumRenewalResponse `json:"renewal"`
	}](t, response).Renewal
}

// renewalRequests: the payment requests the server sent by itself for
// [userID], oldest first.
func renewalRequests(t *testing.T, userID int64) []string {
	t.Helper()
	conn, err := sql.Open(dbDriver, testDSN())
	require.NoError(t, err)
	defer conn.Close()
	rows, err := conn.QueryContext(context.Background(),
		`SELECT id FROM payments WHERE user_id = $1 AND renewal ORDER BY created_at`, userID)
	require.NoError(t, err)
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	return ids
}

func TestAPremiumPaidByMobileMoneyIsAskedAgainBeforeItsEnd(t *testing.T) {
	w := newPaymentsWorld(t)
	sender := &fakeSender{}
	w.server.push = sender
	m := registerMember(t, w.router, nil)
	addPhone(t, w.router, m, "phone-"+uniqueName())

	// a month, to renew
	month := w.pay(t, m.Token, "/v1/premium/payments", map[string]any{
		"plan": "monthly", "phone_number": "+237670000021", "renew": true,
	})
	require.Nil(t, w.renewalOf(t, m.Token), "nothing kept before the money arrived")
	w.notify(t, "payment.completed", month.ID, map[string]any{"amount": 1500})
	_, end := w.premiumOf(t, m.Token)
	require.NotNil(t, end)
	renewal := w.renewalOf(t, m.Token)
	require.NotNil(t, renewal)
	require.Equal(t, "monthly", renewal.Plan)
	require.Equal(t, "+237670000021", renewal.PhoneNumber)
	require.Equal(t, "MTN_MOMO_CMR", renewal.Provider)
	day := end.In(renewalZone)
	require.Equal(t, time.Date(day.Year(), day.Month(), day.Day()-1, 10, 0, 0, 0, renewalZone).UTC(), renewal.NextRequestAt)

	// before its time, nothing is asked
	w.server.renewPremiums(context.Background(), renewal.NextRequestAt.Add(-time.Minute))
	require.Empty(t, renewalRequests(t, m.ID))

	// its time: the same plan, on the same number, and the member is told
	w.server.renewPremiums(context.Background(), renewal.NextRequestAt.Add(time.Minute))
	requests := renewalRequests(t, m.ID)
	require.Len(t, requests, 1)
	asked := w.money.inits[requests[0]]
	require.Equal(t, int64(1500), asked.Amount)
	require.Equal(t, "237670000021", asked.PhoneNumber)
	require.Equal(t, "MTN_MOMO_CMR", asked.Provider)
	require.Len(t, sender.messages, 1)
	require.Equal(t, "Renouvelle ton Premium", sender.messages[0].Title)
	require.Equal(t, "Une demande de 1 500 FCFA arrive sur ton téléphone : saisis ton code mobile money pour continuer Premium.",
		sender.messages[0].Body)
	require.Equal(t, "premium_renewal", sender.messages[0].Data["type"])

	// awaited: never asked twice
	w.server.renewPremiums(context.Background(), renewal.NextRequestAt.Add(30*time.Hour))
	require.Len(t, renewalRequests(t, m.ID), 1)

	// paid: a month more after the end, and the next requests are for it
	w.notify(t, "payment.completed", requests[0], map[string]any{"amount": 1500})
	_, renewed := w.premiumOf(t, m.Token)
	require.WithinDuration(t, end.AddDate(0, 1, 0), *renewed, time.Second)
	require.Len(t, sender.messages, 2)
	require.Equal(t, "Premium renouvelé", sender.messages[1].Title)
	require.Contains(t, sender.messages[1].Body, renewed.In(renewalZone).Format("02/01/2006"))
	next := w.renewalOf(t, m.Token)
	require.NotNil(t, next)
	require.True(t, next.NextRequestAt.After(renewed.AddDate(0, 0, -2)))

	// stopped: Premium goes on, nothing more is asked
	stop := send(t, w.router, http.MethodDelete, m.Token, "/v1/premium/renewal", nil)
	require.Equal(t, http.StatusNoContent, stop.Code)
	require.Nil(t, w.renewalOf(t, m.Token))
	w.server.renewPremiums(context.Background(), next.NextRequestAt.Add(time.Hour))
	require.Len(t, renewalRequests(t, m.ID), 1)
}

func TestARenewalUnansweredThreeTimesLetsPremiumEnd(t *testing.T) {
	w := newPaymentsWorld(t)
	sender := &fakeSender{}
	w.server.push = sender
	m := registerMember(t, w.router, nil)
	addPhone(t, w.router, m, "phone-"+uniqueName())
	year := w.pay(t, m.Token, "/v1/premium/payments", map[string]any{
		"plan": "yearly", "phone_number": "+237690000022", "renew": true,
	})
	w.notify(t, "payment.completed", year.ID, map[string]any{"amount": 12000})
	first := w.renewalOf(t, m.Token).NextRequestAt

	// the day before, the day of the end, the day after: each refused
	now := first.Add(time.Minute)
	for i := 1; i <= maxRenewalAttempts; i++ {
		w.server.renewPremiums(context.Background(), now)
		requests := renewalRequests(t, m.ID)
		require.Len(t, requests, i)
		require.Equal(t, "ORANGE_CMR", w.money.inits[requests[i-1]].Provider)
		w.notify(t, "payment.failed", requests[i-1], map[string]any{"failureCode": "PAYMENT_NOT_APPROVED"})
		now = now.Add(24 * time.Hour)
	}

	// then the member is told, and nothing more is asked
	w.server.renewPremiums(context.Background(), now)
	require.Len(t, renewalRequests(t, m.ID), maxRenewalAttempts)
	require.Nil(t, w.renewalOf(t, m.Token))
	last := sender.messages[len(sender.messages)-1]
	require.Equal(t, "Premium n'a pas pu être renouvelé", last.Title)
	require.Equal(t, "premium_renewal", last.Data["type"])
}

func TestOnlyAPlanPaidByMobileMoneyRenews(t *testing.T) {
	w := newPaymentsWorld(t)
	m := registerMember(t, w.router, nil)

	lifetime := send(t, w.router, http.MethodPost, m.Token, "/v1/premium/payments", map[string]any{
		"plan": "lifetime", "phone_number": "+237670000023", "renew": true,
	})
	require.Equal(t, http.StatusBadRequest, lifetime.Code)
	require.Contains(t, lifetime.Body.String(), "RENEWAL_NEEDS_MOBILE_MONEY")
	card := send(t, w.router, http.MethodPost, m.Token, "/v1/premium/payments", map[string]any{
		"plan": "monthly", "method": "card", "renew": true,
	})
	require.Equal(t, http.StatusBadRequest, card.Code)

	// without asking, nothing is kept
	month := w.pay(t, m.Token, "/v1/premium/payments", map[string]any{"plan": "monthly", "phone_number": "+237670000023"})
	w.notify(t, "payment.completed", month.ID, map[string]any{"amount": 1500})
	require.Nil(t, w.renewalOf(t, m.Token))

	// Premium for life: the renewal has nothing left to do
	renewed := w.pay(t, m.Token, "/v1/premium/payments", map[string]any{
		"plan": "monthly", "phone_number": "+237670000023", "renew": true,
	})
	w.notify(t, "payment.completed", renewed.ID, map[string]any{"amount": 1500})
	renewal := w.renewalOf(t, m.Token)
	require.NotNil(t, renewal)
	forever := w.pay(t, m.Token, "/v1/premium/payments", map[string]any{"plan": "lifetime", "phone_number": "+237670000023"})
	w.notify(t, "payment.completed", forever.ID, map[string]any{"amount": 50000})
	w.server.renewPremiums(context.Background(), renewal.NextRequestAt.Add(time.Minute))
	require.Nil(t, w.renewalOf(t, m.Token))
	require.Empty(t, renewalRequests(t, m.ID))
}
