// Package kpay talks to KPAY-NOW (https://kpay.site/documentation):
// mobile money payments pushed to the members' phones (USSD: they confirm
// with their PIN), card payments on KPAY's hosted page (Visa, Mastercard,
// priced in XAF and charged in USD after conversion), a payment read back,
// refunds, sendings to an organizer, the balance, the commissions. One address
// for the sandbox and production: the keys tell (kpay_test_… or kpay_live_…).
// Webhooks are signed: [VerifySignature].
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
	"regexp"
	"strings"
	"time"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/money"
)

const DefaultURL = "https://admin.kpay.site"

// Client of the API, with the keys of the application (the same address for
// the sandbox and production).
type Client struct {
	BaseURL   string
	APIKey    string
	SecretKey string
	HTTP      *http.Client
}

func New(baseURL, apiKey, secretKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	return &Client{
		BaseURL:   strings.TrimSuffix(baseURL, "/"),
		APIKey:    apiKey,
		SecretKey: secretKey,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

// transaction: a payment, refund or withdrawal as KPAY writes it.
type transaction struct {
	ID                string  `json:"id"`
	Reference         string  `json:"reference"`
	ProviderReference string  `json:"providerReference"`
	Status            string  `json:"status"`
	Amount            float64 `json:"amount"`
	NetAmount         float64 `json:"netAmount"`
	FeeAmount         float64 `json:"feeAmount"`
	Currency          string  `json:"currency"`
	ExternalID        string  `json:"externalId"`
	GatewayURL        string  `json:"gatewayUrl"`
	FailureCode       string  `json:"failureCode"`
	FailureReason     string  `json:"failureReason"`
}

// Init asks for a payment: on the member's phone (USSD: the operator and
// the number), or on KPAY's hosted page for a card ([money.CardPayment]:
// the return address, never the number). Our id is its externalId, which
// makes a retry harmless (409: [money.ErrDuplicate]).
func (c *Client) Init(ctx context.Context, req money.InitRequest) (money.Payment, error) {
	currency := req.Currency
	if currency == "" {
		currency = "XAF"
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		// KPAY refuses a card payment without one (DESCRIPTION_REQUIRED)
		description = "CMFI Connect"
	}
	body := map[string]any{
		"amount":      req.Amount,
		"currency":    currency,
		"externalId":  req.ExternalID,
		"description": description,
	}
	if len(req.Metadata) > 0 {
		body["metadata"] = req.Metadata
	}
	if req.Email != "" {
		body["customerEmail"] = req.Email
	}
	if req.PaymentMethod == money.CardPayment {
		body["paymentMethod"] = money.CardPayment
		// KPAY adds the signed status to it (status, reference, externalId)
		body["returnUrl"] = req.ReturnURL
	} else {
		body["provider"] = req.Provider
		body["phoneNumber"] = local(req.PhoneNumber)
	}
	var answer transaction
	if err := c.call(ctx, http.MethodPost, "/api/v1/payments/init", body, &answer); err != nil {
		return money.Payment{}, err
	}
	return money.Payment{
		ID: answer.ID, Reference: answer.Reference, Status: status(answer.Status), GatewayURL: answer.GatewayURL,
	}, nil
}

// Lookup: where payment [id] (KPAY's id or reference) is, read from KPAY: a
// webhook that may never have come.
func (c *Client) Lookup(ctx context.Context, id string) (money.Webhook, error) {
	var answer transaction
	if err := c.call(ctx, http.MethodGet, "/api/v1/payments/"+url.PathEscape(id), nil, &answer); err != nil {
		return money.Webhook{}, err
	}
	return hookOf("payment", answer), nil
}

// Refund gives the whole payment [paymentID] (KPAY's id) back to its payer,
// within 7 days, from the balance of the wallet.
func (c *Client) Refund(ctx context.Context, paymentID string, req money.RefundRequest) (money.Refund, error) {
	var answer transaction
	err := c.call(ctx, http.MethodPost, "/api/v1/payments/"+url.PathEscape(paymentID)+"/refund", map[string]any{
		"reason": req.Reason, "externalId": req.ExternalID,
	}, &answer)
	return money.Refund{ID: answer.ID, Status: status(answer.Status)}, err
}

// Withdraw sends money from the wallet to a mobile money account (100 XAF
// at least, KPAY keeps its commission from it): [money.ErrBalanceTooLow]
// when the wallet can't cover it (422).
func (c *Client) Withdraw(ctx context.Context, req money.WithdrawRequest) (money.Withdrawal, error) {
	body := map[string]any{
		"amount":      req.Amount,
		"provider":    req.Provider,
		"phoneNumber": local(req.PhoneNumber),
		"externalId":  req.ExternalID,
		"description": req.Description,
	}
	if first, last, ok := strings.Cut(strings.TrimSpace(req.Name), " "); ok {
		body["recipientFirstName"], body["recipientLastName"] = first, strings.TrimSpace(last)
	}
	var answer transaction
	err := c.call(ctx, http.MethodPost, "/api/v1/payments/withdraw", body, &answer)
	if refused := (*money.RefusedError)(nil); errors.As(err, &refused) && refused.Status == http.StatusUnprocessableEntity {
		return money.Withdrawal{}, money.ErrBalanceTooLow
	}
	return money.Withdrawal{
		ID: answer.ID, Status: status(answer.Status), NetAmount: answer.NetAmount, FeeAmount: answer.FeeAmount,
	}, err
}

// Balances of the wallet, in each currency.
func (c *Client) Balances(ctx context.Context) ([]money.Balance, error) {
	var answer []struct {
		Currency         string  `json:"currency"`
		Balance          float64 `json:"balance"`
		ReservedBalance  float64 `json:"reservedBalance"`
		AvailableBalance float64 `json:"availableBalance"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v1/payments/balance", nil, &answer); err != nil {
		return nil, err
	}
	balances := []money.Balance{}
	for _, b := range answer {
		balances = append(balances, money.Balance{
			Currency: strings.ToUpper(b.Currency), Balance: b.Balance,
			ReservedBalance: b.ReservedBalance, AvailableBalance: b.AvailableBalance,
		})
	}
	return balances, nil
}

// PlatformFees: the commissions of the moment (public).
func (c *Client) PlatformFees(ctx context.Context) (money.Fees, error) {
	var answer struct {
		Fees money.Fees `json:"fees"`
	}
	err := c.call(ctx, http.MethodGet, "/api/public/platform-info", nil, &answer)
	return answer.Fees, err
}

// PredictProvider: the operator of a phone number and the number as KPAY
// writes it (237670000000); [money.ErrInvalidPhone] when KPAY doesn't know
// it (in the sandbox, only its test numbers).
func (c *Client) PredictProvider(ctx context.Context, phoneNumber string) (money.Prediction, error) {
	var answer struct {
		Country     string `json:"country"`
		Provider    string `json:"provider"`
		PhoneNumber string `json:"phoneNumber"`
	}
	err := c.call(ctx, http.MethodPost, "/api/v1/payments/predict-provider",
		map[string]string{"phoneNumber": local(phoneNumber)}, &answer)
	if refused := (*money.RefusedError)(nil); errors.As(err, &refused) && refused.Status == http.StatusBadRequest {
		return money.Prediction{}, money.ErrInvalidPhone
	}
	if err != nil {
		return money.Prediction{}, err
	}
	return money.Prediction{Country: answer.Country, Provider: answer.Provider, PhoneNumber: answer.PhoneNumber}, nil
}

// local: a number as KPAY takes it, digits only with the country code
// ("+237 6 70…" -> "237670…"; 9 digits get Cameroon's 237).
func local(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := strings.TrimPrefix(b.String(), "00")
	if len(digits) == 9 {
		digits = "237" + digits
	}
	return digits
}

// status: KPAY's status in the API's words (SUBMITTED: on its way).
func status(s string) string {
	switch strings.ToUpper(s) {
	case "COMPLETED", "SUCCESS", "SUCCESSFUL":
		return money.Completed
	case "FAILED", "EXPIRED":
		return money.Failed
	case "CANCELLED", "CANCELED":
		return money.Cancelled
	case "PROCESSING", "SUBMITTED":
		return money.Processing
	default:
		return money.Pending
	}
}

// phoneLike: 8 digits or more in a row, with a + before them or not.
var phoneLike = regexp.MustCompile(`\+?\d{8,}`)

// hidePhones: an answer of KPAY fit for the logs: no phone number, 1 KB at
// most.
func hidePhones(data []byte) string {
	text := phoneLike.ReplaceAllString(string(data), "***")
	if len(text) > 1024 {
		text = text[:1024] + "…"
	}
	return text
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
		return fmt.Errorf("%w: %v", money.ErrUnknown, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", money.ErrUnknown, err)
	}
	switch {
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: %d", money.ErrUnknown, resp.StatusCode)
	case resp.StatusCode == http.StatusConflict:
		return money.ErrDuplicate
	case resp.StatusCode >= 300:
		var refused struct {
			Message any    `json:"message"`
			Error   string `json:"error"`
			Code    string `json:"code"`
		}
		_ = json.Unmarshal(data, &refused)
		return &money.RefusedError{
			Status: resp.StatusCode, Message: fmt.Sprint(refused.Message),
			Reason: firstOf(refused.Code, refused.Error), Detail: hidePhones(data),
		}
	}
	if answer == nil {
		return nil
	}
	if err := json.Unmarshal(data, answer); err != nil {
		return fmt.Errorf("kpay: unreadable answer: %w", err)
	}
	return nil
}

func firstOf(texts ...string) string {
	for _, text := range texts {
		if strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

// ----- webhooks -----

// VerifySignature: [signature] (X-KPAY-Signature) is the HMAC-SHA256, in hex,
// of the raw [body] with the webhook [secret].
func VerifySignature(body []byte, signature, secret string) bool {
	if secret == "" || strings.TrimSpace(signature) == "" {
		return false
	}
	given, err := hex.DecodeString(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(signature), "sha256=")))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), given)
}

// Sign gives the signature KPAY would send for [body] (for tests).
func Sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ParseWebhook reads a delivery (its signature already checked):
// payment.completed|failed|cancelled, refund.*, payout.*, in the API's words
// already.
func ParseWebhook(body []byte) (money.Webhook, error) {
	var data struct {
		Event string `json:"event"`
		transaction
		PaymentID string `json:"paymentId"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return money.Webhook{}, err
	}
	if data.ID == "" {
		data.ID = data.PaymentID
	}
	kind, _, _ := strings.Cut(data.Event, ".")
	hook := hookOf(kind, data.transaction)
	if data.Event != "" {
		hook.Event = data.Event
	}
	return hook, nil
}

// hookOf: a [kind] (payment, refund, payout) of KPAY as a change of status.
func hookOf(kind string, data transaction) money.Webhook {
	state := status(data.Status)
	hook := money.Webhook{
		Event: kind + "." + strings.ToLower(state), PaymentID: data.ID, Reference: firstOf(data.ProviderReference, data.Reference),
		Status: state, Amount: data.Amount, ExternalID: data.ExternalID,
	}
	if state == money.Failed || state == money.Cancelled {
		hook.FailureCode = codeOf(data.FailureCode, data.FailureReason)
		hook.FailureReason = data.FailureReason
	}
	return hook
}

// codeOf: a failure as a code the apps translate (PAYER_NOT_FOUND…): KPAY's
// own code, else its reason when it is one, else a sentence it is known to
// write (its lookups give only the sentence); "" otherwise.
func codeOf(code, reason string) string {
	if code = strings.TrimSpace(code); code != "" {
		return strings.ToUpper(code)
	}
	reason = strings.TrimSpace(reason)
	if reason != "" && !strings.Contains(reason, " ") && strings.ToUpper(reason) == reason {
		return reason
	}
	lower := strings.ToLower(reason)
	for _, known := range reasons {
		if strings.Contains(lower, known.words) {
			return known.code
		}
	}
	return ""
}

// reasons: the sentences of KPAY (and of the operators behind it) and the
// codes the apps translate.
var reasons = []struct{ words, code string }{
	{"does not have an account", "PAYER_NOT_FOUND"},
	{"payer not found", "PAYER_NOT_FOUND"},
	{"recipient not found", "RECIPIENT_NOT_FOUND"},
	{"insufficient", "INSUFFICIENT_BALANCE"},
	{"limit", "PAYER_LIMIT_REACHED"},
	{"not approved", "PAYMENT_NOT_APPROVED"},
	{"declined", "PAYMENT_NOT_APPROVED"},
	{"rejected", "PAYMENT_NOT_APPROVED"},
	{"cancel", "CANCELLED"},
	{"expired", "EXPIRED"},
	{"timeout", "EXPIRED"},
	{"timed out", "EXPIRED"},
}
