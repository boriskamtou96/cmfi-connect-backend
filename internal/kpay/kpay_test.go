package kpay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/money"
	"github.com/stretchr/testify/require"
)

// fakeKPay answers [status] and [answer] to every call, and keeps the last
// request (its path, headers and body).
type fakeKPay struct {
	status  int
	answer  string
	path    string
	method  string
	headers http.Header
	body    map[string]any
}

func (f *fakeKPay) server(t *testing.T) *Client {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.path, f.method, f.headers = r.URL.Path, r.Method, r.Header.Clone()
		f.body = nil
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			require.NoError(t, json.Unmarshal(raw, &f.body))
		}
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.answer)
	}))
	t.Cleanup(server.Close)
	return New(server.URL, "kpay_test_key", "kpay_test_secret")
}

func TestAMobileMoneyPaymentIsPushedToThePhoneWithTheKeys(t *testing.T) {
	fake := &fakeKPay{status: http.StatusCreated, answer: `{"id":"pay_1","reference":"KPAY-1","status":"PENDING"}`}
	client := fake.server(t)

	payment, err := client.Init(context.Background(), money.InitRequest{
		Amount: 5000, Currency: "XAF", Provider: "MTN_MOMO_CMR", PhoneNumber: "+237 653 45 67 89",
		ExternalID: "e1", Description: "Inscription", Metadata: map[string]string{"purpose": "event"},
	})
	require.NoError(t, err)
	require.Equal(t, money.Payment{ID: "pay_1", Reference: "KPAY-1", Status: money.Pending}, payment)
	require.Equal(t, "/api/v1/payments/init", fake.path)
	require.Equal(t, "kpay_test_key", fake.headers.Get("X-API-Key"))
	require.Equal(t, "kpay_test_secret", fake.headers.Get("X-Secret-Key"))
	require.Equal(t, "MTN_MOMO_CMR", fake.body["provider"])
	require.Equal(t, "237653456789", fake.body["phoneNumber"])
	require.Equal(t, "XAF", fake.body["currency"])
	require.Equal(t, "e1", fake.body["externalId"])
	require.NotContains(t, fake.body, "paymentMethod")
}

func TestACardPaymentOpensTheHostedPageWithoutPhone(t *testing.T) {
	fake := &fakeKPay{status: http.StatusCreated, answer: `{"id":"pay_2","status":"PENDING","mode":"GATEWAY","gatewayUrl":"https://pay.kpay.site/x"}`}
	client := fake.server(t)

	payment, err := client.Init(context.Background(), money.InitRequest{
		Amount: 10000, Currency: "XAF", PaymentMethod: money.CardPayment,
		ReturnURL: "https://cmfi-connect.com/paiement?payment=e2", ExternalID: "e2", Email: "a@b.c",
	})
	require.NoError(t, err)
	require.Equal(t, "https://pay.kpay.site/x", payment.GatewayURL)
	require.Equal(t, "CARD", fake.body["paymentMethod"])
	require.Equal(t, "https://cmfi-connect.com/paiement?payment=e2", fake.body["returnUrl"])
	// KPAY refuses a card without a description, never sends a phone
	require.Equal(t, "CMFI Connect", fake.body["description"])
	require.NotContains(t, fake.body, "phoneNumber")
	require.NotContains(t, fake.body, "provider")
	require.Equal(t, "a@b.c", fake.body["customerEmail"])
}

func TestTheRefusalsOfKPayAreTold(t *testing.T) {
	fake := &fakeKPay{status: http.StatusConflict, answer: `{"statusCode":409,"message":"Transaction with this externalId already exists"}`}
	client := fake.server(t)
	_, err := client.Init(context.Background(), money.InitRequest{Amount: 1, Provider: "MTN_MOMO_CMR", ExternalID: "e"})
	require.ErrorIs(t, err, money.ErrDuplicate)

	fake.status, fake.answer = http.StatusBadRequest, `{"statusCode":400,"message":"amount must not be less than the configured minimum","error":"Bad Request"}`
	_, err = client.Init(context.Background(), money.InitRequest{Amount: 1, Provider: "MTN_MOMO_CMR", ExternalID: "e"})
	var refused *money.RefusedError
	require.True(t, errors.As(err, &refused))
	require.Equal(t, http.StatusBadRequest, refused.Status)

	fake.status = http.StatusBadGateway
	_, err = client.Init(context.Background(), money.InitRequest{Amount: 1, Provider: "MTN_MOMO_CMR", ExternalID: "e"})
	require.ErrorIs(t, err, money.ErrUnknown)

	fake.status, fake.answer = http.StatusUnprocessableEntity, `{"statusCode":422,"message":"Insufficient balance"}`
	_, err = client.Withdraw(context.Background(), money.WithdrawRequest{Amount: 5000, Provider: "MTN_MOMO_CMR", PhoneNumber: "237653456789"})
	require.ErrorIs(t, err, money.ErrBalanceTooLow)

	fake.status, fake.answer = http.StatusBadRequest, `{"statusCode":400,"message":"unknown number"}`
	_, err = client.PredictProvider(context.Background(), "699999999")
	require.ErrorIs(t, err, money.ErrInvalidPhone)
}

func TestAPaymentIsReadBackWhenItsWebhookNeverCame(t *testing.T) {
	fake := &fakeKPay{status: http.StatusOK, answer: `{"id":"pay_3","reference":"KPAY-3","providerReference":"ref_3",
		"status":"FAILED","amount":5000,"externalId":"e3","failureCode":"PAYER_NOT_FOUND","failureReason":"Payer not found"}`}
	client := fake.server(t)

	hook, err := client.Lookup(context.Background(), "pay_3")
	require.NoError(t, err)
	require.Equal(t, "/api/v1/payments/pay_3", fake.path)
	require.Equal(t, http.MethodGet, fake.method)
	require.Equal(t, "payment.failed", hook.Event)
	require.Equal(t, money.Failed, hook.Status)
	require.Equal(t, "e3", hook.ExternalID)
	require.Equal(t, "ref_3", hook.Reference)
	require.Equal(t, "PAYER_NOT_FOUND", hook.FailureCode)

	fake.answer = `{"id":"pay_3","status":"SUBMITTED"}`
	hook, err = client.Lookup(context.Background(), "pay_3")
	require.NoError(t, err)
	require.Equal(t, money.Processing, hook.Status)
}

func TestRefundWithdrawBalanceAndFees(t *testing.T) {
	fake := &fakeKPay{status: http.StatusCreated, answer: `{"id":"rf_1","status":"PENDING"}`}
	client := fake.server(t)
	refund, err := client.Refund(context.Background(), "pay_1", money.RefundRequest{Reason: "admin", ExternalID: "r1"})
	require.NoError(t, err)
	require.Equal(t, money.Refund{ID: "rf_1", Status: money.Pending}, refund)
	require.Equal(t, "/api/v1/payments/pay_1/refund", fake.path)
	require.Equal(t, "r1", fake.body["externalId"])

	fake.answer = `{"id":"wd_1","status":"PENDING","netAmount":4750,"feeAmount":250}`
	sent, err := client.Withdraw(context.Background(), money.WithdrawRequest{
		Amount: 5000, Provider: "ORANGE_CMR", PhoneNumber: "699000000", Name: "Jean Paul Mbarga", ExternalID: "w1",
	})
	require.NoError(t, err)
	require.Equal(t, money.Withdrawal{ID: "wd_1", Status: money.Pending, NetAmount: 4750, FeeAmount: 250}, sent)
	require.Equal(t, "237699000000", fake.body["phoneNumber"])
	require.Equal(t, "Jean", fake.body["recipientFirstName"])
	require.Equal(t, "Paul Mbarga", fake.body["recipientLastName"])

	fake.status, fake.answer = http.StatusOK, `[{"currency":"xaf","balance":150000,"reservedBalance":25000,"availableBalance":125000}]`
	balances, err := client.Balances(context.Background())
	require.NoError(t, err)
	require.Equal(t, []money.Balance{{Currency: "XAF", Balance: 150000, ReservedBalance: 25000, AvailableBalance: 125000}}, balances)

	fake.answer = `{"fees":{"paymentRate":0.03,"withdrawalRate":0.05}}`
	fees, err := client.PlatformFees(context.Background())
	require.NoError(t, err)
	require.Equal(t, money.Fees{PaymentRate: 0.03, WithdrawalRate: 0.05}, fees)
}

func TestOnlyASignedWebhookIsBelieved(t *testing.T) {
	body := []byte(`{"event":"payment.completed","paymentId":"pay_1","reference":"KPAY-1","status":"COMPLETED","amount":5000,"externalId":"e1"}`)
	signature := Sign(body, "whsec")
	require.True(t, VerifySignature(body, signature, "whsec"))
	require.True(t, VerifySignature(body, "sha256="+signature, "whsec"))
	require.False(t, VerifySignature(body, signature, "other"))
	require.False(t, VerifySignature(append(body, ' '), signature, "whsec"))
	require.False(t, VerifySignature(body, "", "whsec"))
	require.False(t, VerifySignature(body, signature, ""))

	hook, err := ParseWebhook(body)
	require.NoError(t, err)
	require.Equal(t, money.Webhook{
		Event: "payment.completed", PaymentID: "pay_1", Reference: "KPAY-1", Status: money.Completed,
		Amount: float64(5000), ExternalID: "e1",
	}, hook)

	hook, err = ParseWebhook([]byte(`{"event":"payout.failed","paymentId":"wd_1","status":"FAILED","externalId":"w1","failureReason":"RECIPIENT_NOT_FOUND"}`))
	require.NoError(t, err)
	require.Equal(t, "payout.failed", hook.Event)
	require.Equal(t, "RECIPIENT_NOT_FOUND", hook.FailureCode)

	hook, err = ParseWebhook([]byte(`{"event":"payment.cancelled","paymentId":"pay_2","status":"CANCELLED","externalId":"e2"}`))
	require.NoError(t, err)
	require.Equal(t, money.Cancelled, hook.Status)
}
