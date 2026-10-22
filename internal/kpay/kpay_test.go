package kpay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPaymentIsSentWithTheKeys(t *testing.T) {
	var got InitRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/payments/init", r.URL.Path)
		require.Equal(t, "kpay_test_x", r.Header.Get("X-API-Key"))
		require.Equal(t, "secret", r.Header.Get("X-Secret-Key"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"pay_1","reference":"KPAY-1","status":"PENDING"}`))
	}))
	defer server.Close()

	payment, err := New(server.URL, "kpay_test_x", "secret").Init(context.Background(), InitRequest{
		Amount: 5000, Currency: "XAF", Provider: "MTN_MOMO_CMR", PhoneNumber: "237653456789", ExternalID: "e1",
	})
	require.NoError(t, err)
	require.Equal(t, Payment{ID: "pay_1", Reference: "KPAY-1", Status: Pending}, payment)
	require.Equal(t, int64(5000), got.Amount)
	require.Equal(t, "e1", got.ExternalID)
}

func TestTheAnswersOfKPay(t *testing.T) {
	status := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"statusCode":400,"message":"amount must not be less than the configured minimum"}`))
	}))
	defer server.Close()
	client := New(server.URL, "k", "s")

	status = http.StatusBadRequest
	_, err := client.Init(context.Background(), InitRequest{})
	var refused *RefusedError
	require.ErrorAs(t, err, &refused)
	require.Contains(t, refused.Message, "minimum")

	status = http.StatusConflict
	_, err = client.Init(context.Background(), InitRequest{})
	require.ErrorIs(t, err, ErrDuplicate)

	status = http.StatusBadGateway
	_, err = client.Init(context.Background(), InitRequest{})
	require.True(t, errors.Is(err, ErrUnknown))

	status = http.StatusBadRequest
	_, err = client.PredictProvider(context.Background(), "12")
	require.ErrorIs(t, err, ErrInvalidPhone)
}

func TestOnlyASignedWebhookIsBelieved(t *testing.T) {
	body := []byte(`{"event":"payment.completed","externalId":"e1","status":"COMPLETED"}`)
	signature := Sign(body, "whsec")
	require.True(t, VerifySignature(body, signature, "whsec"))
	require.False(t, VerifySignature(body, signature, "other"))
	require.False(t, VerifySignature([]byte(`{"event":"payment.completed","externalId":"e2"}`), signature, "whsec"))
	require.False(t, VerifySignature(body, "", "whsec"))
	require.False(t, VerifySignature(body, signature, ""))
}
