package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func payoutsPath(event eventResponse, suffix string) string {
	return fmt.Sprintf("/v1/admin/events/%d%s", event.ID, suffix)
}

func (w paymentsWorld) overview(t *testing.T, token string, event eventResponse) payoutsOverview {
	t.Helper()
	response := send(t, w.router, http.MethodGet, token, payoutsPath(event, "/payouts"), nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[payoutsOverview](t, response)
}

// confirmAccount gives [event] the Orange account 699000001 and confirms it.
func (w paymentsWorld) confirmAccount(t *testing.T, token string, event eventResponse) {
	t.Helper()
	set := send(t, w.router, http.MethodPut, token, payoutsPath(event, "/payout-account"),
		map[string]any{"phone_number": "+237 6 99 00 00 01", "holder": "Comité du camp"})
	require.Equal(t, http.StatusOK, set.Code, set.Body.String())
	for id, req := range w.money.withdrawalsTo("237699000001") {
		if req.Amount == payoutTestAmount {
			w.notify(t, "payout.completed", id, nil)
		}
	}
}

func TestAnEventAccountIsConfirmedByATestOf100(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 5000})

	set := send(t, w.router, http.MethodPut, admin.Token, payoutsPath(event, "/payout-account"),
		map[string]any{"phone_number": "+237 6 99 00 00 01", "holder": "Comité du camp"})
	require.Equal(t, http.StatusOK, set.Code, set.Body.String())
	overview := decode[payoutsOverview](t, set)
	require.Equal(t, "237699000001", overview.Account.PhoneNumber)
	require.Equal(t, "ORANGE_CMR", overview.Account.Provider)
	require.Nil(t, overview.Account.ConfirmedAt)
	require.Equal(t, "pending", overview.Account.Test.Status)
	require.Equal(t, int64(100), overview.Account.Test.Amount)
	require.Equal(t, int64(95), overview.Account.Test.Received)

	// the 100 FCFA arrived: confirmed
	for id := range w.money.withdrawalsTo("237699000001") {
		w.notify(t, "payout.completed", id, nil)
	}
	require.NotNil(t, w.overview(t, admin.Token, event).Account.ConfirmedAt)

	// another number: to confirm again
	changed := send(t, w.router, http.MethodPut, admin.Token, payoutsPath(event, "/payout-account"),
		map[string]any{"phone_number": "+237 6 70 00 00 02", "holder": "Trésorier"})
	require.Equal(t, http.StatusOK, changed.Code, changed.Body.String())
	require.Nil(t, decode[payoutsOverview](t, changed).Account.ConfirmedAt)

	// a failed test says why, and can be sent again
	for id := range w.money.withdrawalsTo("237670000002") {
		w.notify(t, "payout.failed", id, map[string]any{"failureCode": "RECIPIENT_NOT_FOUND"})
	}
	test := w.overview(t, admin.Token, event).Account.Test
	require.Equal(t, "failed", test.Status)
	require.Equal(t, "RECIPIENT_NOT_FOUND", test.FailureCode)
	again := send(t, w.router, http.MethodPost, admin.Token, payoutsPath(event, "/payout-account/test"), nil)
	require.Equal(t, http.StatusOK, again.Code, again.Body.String())
	require.Equal(t, "pending", decode[payoutsOverview](t, again).Account.Test.Status)
}

func TestTheMoneyOfAnEventIsForwardedToItsAccount(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 5000})
	for _, phone := range []string{"+237670000021", "+237670000022"} {
		m := registerMember(t, w.router, nil)
		payment := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{"phone_number": phone})
		w.notify(t, "payment.completed", payment.ID, map[string]any{"amount": 5000})
	}

	// not before the account is confirmed
	early := send(t, w.router, http.MethodPost, admin.Token, payoutsPath(event, "/payouts"), nil)
	require.Equal(t, http.StatusConflict, early.Code)
	require.Contains(t, early.Body.String(), "PAYOUT_ACCOUNT_NOT_CONFIRMED")
	w.confirmAccount(t, admin.Token, event)

	// 2 × 5 000, KPAY's 5 % kept: 9 500 in the wallet for the event
	overview := w.overview(t, admin.Token, event)
	require.Equal(t, toForwardResponse{Payments: 2, Amount: 10000, Net: 9500, Currency: "XAF"}, overview.ToForward)

	// the wallet holds less: said, nothing sent
	w.money.available = 2000
	short := send(t, w.router, http.MethodPost, admin.Token, payoutsPath(event, "/payouts"), nil)
	require.Equal(t, http.StatusConflict, short.Code)
	require.Contains(t, short.Body.String(), "WALLET_BALANCE_TOO_LOW")
	w.money.available = 1_000_000

	sent := send(t, w.router, http.MethodPost, admin.Token, payoutsPath(event, "/payouts"), nil)
	require.Equal(t, http.StatusOK, sent.Code, sent.Body.String())
	overview = decode[payoutsOverview](t, sent)
	require.Equal(t, 0, overview.ToForward.Payments)
	require.Len(t, overview.Payouts, 1)
	require.Equal(t, int64(9500), overview.Payouts[0].Amount)
	require.Equal(t, "Comité du camp", overview.Payouts[0].Holder)

	// it failed: the payments wait for the next sending
	w.notify(t, "payout.failed", overview.Payouts[0].ID, map[string]any{"failureCode": "UNSPECIFIED_FAILURE"})
	require.Equal(t, 2, w.overview(t, admin.Token, event).ToForward.Payments)

	// sent again, arrived
	sent = send(t, w.router, http.MethodPost, admin.Token, payoutsPath(event, "/payouts"), nil)
	require.Equal(t, http.StatusOK, sent.Code, sent.Body.String())
	latest := decode[payoutsOverview](t, sent).Payouts[0]
	w.notify(t, "payout.completed", latest.ID, nil)
	overview = w.overview(t, admin.Token, event)
	require.Equal(t, "completed", overview.Payouts[0].Status)
	require.Equal(t, 0, overview.ToForward.Payments)

	// the payments say they were forwarded; nothing left to send
	listed := decode[[]adminPaymentResponse](t, send(t, w.router, http.MethodGet, admin.Token,
		fmt.Sprintf("/v1/admin/payments?event_id=%d", event.ID), nil))
	require.True(t, listed[0].Forwarded)
	nothing := send(t, w.router, http.MethodPost, admin.Token, payoutsPath(event, "/payouts"), nil)
	require.Equal(t, http.StatusConflict, nothing.Code)
	require.Contains(t, nothing.Body.String(), "NOTHING_TO_FORWARD")
}
