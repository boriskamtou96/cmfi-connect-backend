package wajub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/money"
	"github.com/stretchr/testify/require"
)

// asked: a request the fake Wajub got.
type asked struct {
	method, path, auth, idempotency string
	body                            map[string]any
}

// fakeWajub answers [answers] by "METHOD path" and keeps what it was asked.
func fakeWajub(t *testing.T, answers map[string]func() (int, string)) (*Client, *[]asked) {
	t.Helper()
	got := []asked{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		got = append(got, asked{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), body})
		answer, ok := answers[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		status, text := answer()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(text))
	}))
	t.Cleanup(server.Close)
	return New(server.URL, "sk_test.secret", money.Fees{PaymentRate: 0.04, WithdrawalRate: 0.03}), &got
}

func answer(status int, text string) func() (int, string) {
	return func() (int, string) { return status, text }
}

func TestAMobileMoneyPaymentIsCreatedThenChargedOnTheOperator(t *testing.T) {
	client, got := fakeWajub(t, map[string]func() (int, string){
		"POST /payments":       answer(201, `{"code":201,"status":"success","transaction":{"id":"pay_1","reference":"our-id","status":"pending"}}`),
		"POST /payments/pay_1": answer(200, `{"code":200,"status":"success","transaction":{"id":"pay_1","status":"processing"}}`),
	})
	payment, err := client.Init(context.Background(), money.InitRequest{
		Amount: 1500, Currency: "XAF", Provider: "ORANGE_CMR", PhoneNumber: "237690000001",
		ExternalID: "our-id", Description: "Premium", Metadata: map[string]string{"purpose": "premium"},
	})
	require.NoError(t, err)
	require.Equal(t, money.Payment{ID: "pay_1", Reference: "our-id", Status: money.Processing}, payment)

	require.Len(t, *got, 2)
	created, charged := (*got)[0], (*got)[1]
	require.Equal(t, "sk_test.secret", created.auth)
	require.Equal(t, "our-id:create", created.idempotency)
	require.Equal(t, float64(1500), created.body["amount"])
	require.Equal(t, "our-id", created.body["reference"])
	require.Equal(t, map[string]any{"phone": "+237690000001"}, created.body["customer"])
	require.Equal(t, map[string]any{"channels": []any{"cm.orange"}}, created.body["accept"])
	require.Equal(t, "our-id:charge", charged.idempotency)
	require.Equal(t, "cm.orange", charged.body["channel"])
	require.Equal(t, map[string]any{"phone": "+237690000001"}, charged.body["data"])
}

func TestACardPaymentOpensWajubsPage(t *testing.T) {
	client, got := fakeWajub(t, map[string]func() (int, string){
		"POST /payments": answer(201, `{"code":201,"authorization_url":"https://pay.wajub.com/x","transaction":{"id":"pay_2","status":"pending"}}`),
	})
	payment, err := client.Init(context.Background(), money.InitRequest{
		Amount: 5000, Currency: "XAF", PaymentMethod: money.CardPayment, Email: "a@b.cm",
		ReturnURL: "https://cmfi-connect.com/paiement?payment=our-id", ExternalID: "our-id",
	})
	require.NoError(t, err)
	require.Equal(t, "https://pay.wajub.com/x", payment.GatewayURL)
	require.Len(t, *got, 1)
	require.Equal(t, map[string]any{"email": "a@b.cm"}, (*got)[0].body["customer"])
	require.Equal(t, map[string]any{"channels": []any{"card"}}, (*got)[0].body["accept"])
	require.Equal(t, "https://cmfi-connect.com/paiement?payment=our-id", (*got)[0].body["callback"])
}

func TestRefusalsAndUnknownOutcomes(t *testing.T) {
	client, _ := fakeWajub(t, map[string]func() (int, string){
		"POST /payments": answer(422, `{"code":422,"status":"error","message":"The phone field is invalid."}`),
		"POST /refunds":  answer(503, `oops`),
	})
	_, err := client.Init(context.Background(), money.InitRequest{Amount: 100, Currency: "XAF", Provider: "MTN_MOMO_CMR", PhoneNumber: "237670000000", ExternalID: "x"})
	var refused *money.RefusedError
	require.ErrorAs(t, err, &refused)
	require.Equal(t, 422, refused.Status)
	require.Contains(t, refused.Message, "phone")

	_, err = client.Refund(context.Background(), "pay_1", money.RefundRequest{ExternalID: "r"})
	require.ErrorIs(t, err, money.ErrUnknown)

	_, err = client.Init(context.Background(), money.InitRequest{Provider: "AIRTEL_CMR", ExternalID: "y"})
	require.ErrorAs(t, err, &refused)
}

func TestRefundTransferBalanceAndLookup(t *testing.T) {
	client, got := fakeWajub(t, map[string]func() (int, string){
		"POST /refunds":       answer(201, `{"refund":{"id":"re_1","status":"pending"}}`),
		"POST /transfers":     answer(201, `{"transfer":{"id":"tr_1","status":"processing","amount":9500}}`),
		"GET /balance":        answer(200, `{"balance":[{"currency":"xaf","available":12000,"pending":500}]}`),
		"GET /payments/pay_9": answer(200, `{"transaction":{"id":"pay_9","reference":"our-9","status":"expired","amount":1500}}`),
	})
	ctx := context.Background()
	refund, err := client.Refund(ctx, "pay_1", money.RefundRequest{Reason: "event unavailable", ExternalID: "ref-1"})
	require.NoError(t, err)
	require.Equal(t, money.Refund{ID: "re_1", Status: money.Pending}, refund)
	require.Equal(t, map[string]any{"payment": "pay_1", "reason": "service_not_delivered", "reference": "ref-1"}, (*got)[0].body)

	sent, err := client.Withdraw(ctx, money.WithdrawRequest{Amount: 9500, Provider: "MTN_MOMO_CMR", PhoneNumber: "237670000002", Name: "Église", ExternalID: "po-1"})
	require.NoError(t, err)
	require.Equal(t, money.Withdrawal{ID: "tr_1", Status: money.Processing, NetAmount: 9500}, sent)
	require.Equal(t, map[string]any{"name": "Église", "channel": "cm.mtn", "phone": "+237670000002"}, (*got)[1].body["beneficiary"])

	balances, err := client.Balances(ctx)
	require.NoError(t, err)
	require.Equal(t, []money.Balance{{Currency: "XAF", Balance: 12500, ReservedBalance: 500, AvailableBalance: 12000}}, balances)

	fees, err := client.PlatformFees(ctx)
	require.NoError(t, err)
	require.Equal(t, money.Fees{PaymentRate: 0.04, WithdrawalRate: 0.03}, fees)

	hook, err := client.Lookup(ctx, "pay_9")
	require.NoError(t, err)
	require.Equal(t, "payment.failed", hook.Event)
	require.Equal(t, money.Failed, hook.Status)
	require.Equal(t, "EXPIRED", hook.FailureCode)
	require.Equal(t, "our-9", hook.ExternalID)
}

func TestAShortBalanceStopsATransfer(t *testing.T) {
	client, _ := fakeWajub(t, map[string]func() (int, string){
		"POST /transfers": answer(422, `{"code":422,"status":"error","message":"Insufficient balance."}`),
	})
	_, err := client.Withdraw(context.Background(), money.WithdrawRequest{Amount: 1, Provider: "ORANGE_CMR", PhoneNumber: "237690000000", ExternalID: "po"})
	require.True(t, errors.Is(err, money.ErrBalanceTooLow))
}

func TestTheOperatorIsReadFromTheNumber(t *testing.T) {
	client := New("", "", money.Fees{})
	for phone, provider := range map[string]string{
		"+237 6 70 00 00 00": "MTN_MOMO_CMR", "651234567": "MTN_MOMO_CMR", "00237680000000": "MTN_MOMO_CMR",
		"+237690000000": "ORANGE_CMR", "656000000": "ORANGE_CMR", "237686000000": "ORANGE_CMR",
	} {
		prediction, err := client.PredictProvider(context.Background(), phone)
		require.NoError(t, err, phone)
		require.Equal(t, provider, prediction.Provider, phone)
		require.Len(t, prediction.PhoneNumber, 12, phone)
	}
	for _, phone := range []string{"+33612345678", "222000000", "12", "+237660000000"} {
		_, err := client.PredictProvider(context.Background(), phone)
		require.ErrorIs(t, err, money.ErrInvalidPhone, phone)
	}
}

func TestOnlyASignedRecentWebhookIsBelieved(t *testing.T) {
	body := []byte(`{"event":"payment.succeeded","data":{"id":"pay_1"}}`)
	now := time.Unix(1_800_000_000, 0)
	signature, timestamp := Sign(body, "whsec", now)

	require.True(t, VerifySignature(body, signature, timestamp, "whsec", now.Add(time.Minute)))
	require.False(t, VerifySignature(body, signature, timestamp, "other", now))
	require.False(t, VerifySignature([]byte(`{"event":"payment.succeeded","data":{"id":"pay_2"}}`), signature, timestamp, "whsec", now))
	require.False(t, VerifySignature(body, signature, timestamp, "whsec", now.Add(10*time.Minute)), "replayed")
	require.False(t, VerifySignature(body, signature[3:], timestamp, "whsec", now), "no v1=")
	require.False(t, VerifySignature(body, signature, timestamp, "", now), "no secret")
}

func TestWebhooksAreReadInTheAPIsWords(t *testing.T) {
	for body, want := range map[string]money.Webhook{
		`{"event":"payment.succeeded","data":{"id":"pay_1","reference":"ours","status":"succeeded","amount":1500,"amount_paid":1500}}`: {
			Event: "payment.completed", PaymentID: "pay_1", Reference: "pay_1", ExternalID: "ours", Status: money.Completed, Amount: float64(1500)},
		`{"event":"payment.failed","data":{"id":"pay_2","reference":"ours","status":"failed","failure_reason":"insufficient_funds"}}`: {
			Event: "payment.failed", PaymentID: "pay_2", Reference: "pay_2", ExternalID: "ours", Status: money.Failed, Amount: float64(0),
			FailureCode: "INSUFFICIENT_FUNDS", FailureReason: "INSUFFICIENT_FUNDS"},
		`{"event":"payment.expired","data":{"id":"pay_3","reference":"ours","status":"expired"}}`: {
			Event: "payment.failed", PaymentID: "pay_3", Reference: "pay_3", ExternalID: "ours", Status: money.Failed, Amount: float64(0), FailureCode: "EXPIRED"},
		`{"event":"refund.succeeded","data":{"id":"re_1","reference":"our-refund","status":"succeeded"}}`: {
			Event: "refund.completed", PaymentID: "re_1", ExternalID: "our-refund", Status: money.Completed, Amount: float64(0)},
		`{"event":"transfer.failed","data":{"id":"tr_1","reference":"our-payout","status":"failed","failure_reason":"recipient_not_found"}}`: {
			Event: "payout.failed", PaymentID: "tr_1", ExternalID: "our-payout", Status: money.Failed, Amount: float64(0), FailureReason: "RECIPIENT_NOT_FOUND"},
		`{"event":"customer.created","data":{}}`: {Event: "customer.created"},
	} {
		hook, err := ParseWebhook([]byte(body))
		require.NoError(t, err, body)
		require.Equal(t, want, hook, body)
	}
}
