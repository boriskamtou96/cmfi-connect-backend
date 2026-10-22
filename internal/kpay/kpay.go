// Package kpay talks to KPAY-NOW (https://kpay.site/documentation): mobile
// money payments from the members' phones (USSD: they confirm with their
// PIN), and their refunds. KPAY has no endpoint to read a payment: its final
// status only comes by a webhook signed with the webhook secret
// ([VerifySignature]).
package kpay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultURL = "https://admin.kpay.site"

// The statuses of a payment or a refund.
const (
	Pending    = "PENDING"
	Processing = "PROCESSING"
	Submitted  = "SUBMITTED"
	Completed  = "COMPLETED"
	Failed     = "FAILED"
	Cancelled  = "CANCELLED"
)

var (
	// ErrUnknown: KPAY could not tell what happened (a 5xx, the network). The
	// payment may have started; its webhook will tell.
	ErrUnknown = errors.New("kpay: outcome unknown")
	// ErrDuplicate: this externalId was already sent (a retry): it goes on.
	ErrDuplicate = errors.New("kpay: already sent")
	// ErrInvalidPhone: KPAY does not know that number.
	ErrInvalidPhone = errors.New("kpay: invalid phone number")
	// ErrBalanceTooLow: the wallet can't cover a withdrawal (422).
	ErrBalanceTooLow = errors.New("kpay: wallet balance too low")
)

// RefusedError: KPAY refused the request (4xx), with its reason.
type RefusedError struct {
	Status  int
	Message string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("kpay: %d %s", e.Status, e.Message)
}

// Client of the API, with the keys of the application (kpay_test_… for the
// sandbox, kpay_live_… in production: the same address).
type Client struct {
	BaseURL   string
	APIKey    string
	SecretKey string
	HTTP      *http.Client
}

func New(baseURL, apiKey, secretKey string) *Client {
	return &Client{
		BaseURL:   strings.TrimSuffix(baseURL, "/"),
		APIKey:    apiKey,
		SecretKey: secretKey,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

// CardPayment: the payment method of KPAY's hosted page for Visa and
// Mastercard (charged in USD after conversion, 1 USD at least).
const CardPayment = "CARD"

// InitRequest asks the member's phone for the payment (USSD mode), or opens
// KPAY's hosted page for a card ([CardPayment], with [ReturnURL]: then no
// provider nor phone number).
type InitRequest struct {
	Amount        int64             `json:"amount"`
	Currency      string            `json:"currency,omitempty"`
	Provider      string            `json:"provider,omitempty"`
	PhoneNumber   string            `json:"phoneNumber,omitempty"`
	PaymentMethod string            `json:"paymentMethod,omitempty"`
	ReturnURL     string            `json:"returnUrl,omitempty"`
	ExternalID    string            `json:"externalId"`
	Description   string            `json:"description,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Payment: what KPAY answers to a new payment.
type Payment struct {
	// the id KPAY gives, needed for a refund
	ID        string `json:"id"`
	Reference string `json:"reference"`
	Status    string `json:"status"`
	// the hosted page to send the payer to (a card)
	GatewayURL string `json:"gatewayUrl"`
}

func (c *Client) Init(ctx context.Context, req InitRequest) (Payment, error) {
	var answer Payment
	err := c.call(ctx, http.MethodPost, "/api/v1/payments/init", req, &answer)
	return answer, err
}

type RefundRequest struct {
	Reason     string `json:"reason,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
}

type Refund struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Refund gives the whole payment [paymentID] (KPAY's id) back to its payer;
// within 7 days, from the balance of the wallet.
func (c *Client) Refund(ctx context.Context, paymentID string, req RefundRequest) (Refund, error) {
	var answer Refund
	err := c.call(ctx, http.MethodPost, "/api/v1/payments/"+url.PathEscape(paymentID)+"/refund", req, &answer)
	return answer, err
}

// WithdrawRequest sends money from the wallet to a mobile money account
// (100 to 500 000 XAF; KPAY keeps its commission from it).
type WithdrawRequest struct {
	Amount      int64  `json:"amount"`
	Provider    string `json:"provider"`
	PhoneNumber string `json:"phoneNumber"`
	ExternalID  string `json:"externalId"`
	Description string `json:"description,omitempty"`
}

type Withdrawal struct {
	ID        string  `json:"id"`
	Status    string  `json:"status"`
	NetAmount float64 `json:"netAmount"`
	FeeAmount float64 `json:"feeAmount"`
}

// Withdraw: ErrBalanceTooLow when the wallet can't cover it.
func (c *Client) Withdraw(ctx context.Context, req WithdrawRequest) (Withdrawal, error) {
	var answer Withdrawal
	err := c.call(ctx, http.MethodPost, "/api/v1/payments/withdraw", req, &answer)
	return answer, err
}

// Balance of the wallet in one currency.
type Balance struct {
	Currency         string  `json:"currency"`
	Balance          float64 `json:"balance"`
	ReservedBalance  float64 `json:"reservedBalance"`
	AvailableBalance float64 `json:"availableBalance"`
}

func (c *Client) Balances(ctx context.Context) ([]Balance, error) {
	var answer []Balance
	err := c.call(ctx, http.MethodGet, "/api/v1/payments/balance", nil, &answer)
	return answer, err
}

// Fees: the share KPAY keeps of a payment and of a withdrawal (0.05: 5 %).
type Fees struct {
	PaymentRate    float64 `json:"paymentRate"`
	WithdrawalRate float64 `json:"withdrawalRate"`
}

// PlatformFees: the commissions of the moment (public, no key).
func (c *Client) PlatformFees(ctx context.Context) (Fees, error) {
	var answer struct {
		Fees Fees `json:"fees"`
	}
	err := c.call(ctx, http.MethodGet, "/api/public/platform-info", nil, &answer)
	return answer.Fees, err
}

// Prediction: the operator of a phone number, and the number as KPAY writes
// it (237670000000).
type Prediction struct {
	Country     string `json:"country"`
	Provider    string `json:"provider"`
	PhoneNumber string `json:"phoneNumber"`
}

func (c *Client) PredictProvider(ctx context.Context, phoneNumber string) (Prediction, error) {
	var answer Prediction
	err := c.call(ctx, http.MethodPost, "/api/v1/payments/predict-provider", map[string]string{"phoneNumber": phoneNumber}, &answer)
	var refused *RefusedError
	if errors.As(err, &refused) && refused.Status == http.StatusBadRequest {
		return Prediction{}, ErrInvalidPhone
	}
	return answer, err
}

func (c *Client) call(ctx context.Context, method, path string, body, answer any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.APIKey)
	req.Header.Set("X-Secret-Key", c.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnknown, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnknown, err)
	}
	switch {
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: %d", ErrUnknown, resp.StatusCode)
	case resp.StatusCode == http.StatusConflict:
		return ErrDuplicate
	case resp.StatusCode == http.StatusUnprocessableEntity:
		return ErrBalanceTooLow
	case resp.StatusCode >= 300:
		var refused struct {
			Message any `json:"message"`
		}
		_ = json.Unmarshal(data, &refused)
		return &RefusedError{Status: resp.StatusCode, Message: fmt.Sprint(refused.Message)}
	}
	if err := json.Unmarshal(data, answer); err != nil {
		return fmt.Errorf("kpay: unreadable answer: %w", err)
	}
	return nil
}

// Webhook is a change of status KPAY sends: payment.completed,
// payment.failed, payment.cancelled, refund.completed…
type Webhook struct {
	Event       string `json:"event"`
	PaymentID   string `json:"paymentId"`
	Reference   string `json:"reference"`
	Status      string `json:"status"`
	Amount      any    `json:"amount"`
	ExternalID  string `json:"externalId"`
	FailureCode string `json:"failureCode"`
	// a code (PAYER_NOT_FOUND…) or a sentence
	FailureReason string `json:"failureReason"`
}

// VerifySignature: [signature] (X-KPAY-Signature) is the HMAC-SHA256, in hex,
// of the raw [body] with the webhook [secret].
func VerifySignature(body []byte, signature, secret string) bool {
	if secret == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	given, err := hex.DecodeString(strings.TrimSpace(strings.TrimPrefix(signature, "sha256=")))
	return err == nil && hmac.Equal(expected, given)
}

// Sign gives the signature KPAY would send for [body] (for tests).
func Sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Operator is a mobile money operator KPAY reaches.
type Operator struct {
	Provider string
	Name     string
	Country  string
	Names    map[string]string
	Prefix   string
	Currency string
	// the least KPAY takes, in the currency ("" when not said)
	MinAmount string
}

// Operators: the catalogue of KPAY (the providers of POST
// /api/v1/payments/init), with the names shown to the members.
var Operators = []Operator{
	{"MTN_MOMO_CMR", "MTN", "CMR", map[string]string{"fr": "Cameroun", "en": "Cameroon"}, "237", "XAF", "50"},
	{"ORANGE_CMR", "Orange", "CMR", map[string]string{"fr": "Cameroun", "en": "Cameroon"}, "237", "XAF", "50"},
	{"AIRTEL_GAB", "Airtel", "GAB", map[string]string{"fr": "Gabon", "en": "Gabon"}, "241", "XAF", ""},
	{"AIRTEL_COG", "Airtel", "COG", map[string]string{"fr": "Congo", "en": "Congo"}, "242", "XAF", ""},
	{"MTN_MOMO_COG", "MTN", "COG", map[string]string{"fr": "Congo", "en": "Congo"}, "242", "XAF", ""},
	{"MTN_MOMO_BEN", "MTN", "BEN", map[string]string{"fr": "Bénin", "en": "Benin"}, "229", "XOF", ""},
	{"MOOV_BEN", "Moov", "BEN", map[string]string{"fr": "Bénin", "en": "Benin"}, "229", "XOF", ""},
	{"MTN_MOMO_CIV", "MTN", "CIV", map[string]string{"fr": "Côte d'Ivoire", "en": "Ivory Coast"}, "225", "XOF", ""},
	{"ORANGE_CIV", "Orange", "CIV", map[string]string{"fr": "Côte d'Ivoire", "en": "Ivory Coast"}, "225", "XOF", ""},
	{"FREE_SEN", "Free", "SEN", map[string]string{"fr": "Sénégal", "en": "Senegal"}, "221", "XOF", ""},
	{"ORANGE_SEN", "Orange", "SEN", map[string]string{"fr": "Sénégal", "en": "Senegal"}, "221", "XOF", ""},
	{"VODACOM_MPESA_COD", "Vodacom M-Pesa", "COD", map[string]string{"fr": "RD Congo", "en": "DR Congo"}, "243", "CDF", ""},
	{"AIRTEL_COD", "Airtel", "COD", map[string]string{"fr": "RD Congo", "en": "DR Congo"}, "243", "CDF", ""},
	{"ORANGE_COD", "Orange", "COD", map[string]string{"fr": "RD Congo", "en": "DR Congo"}, "243", "CDF", ""},
}
