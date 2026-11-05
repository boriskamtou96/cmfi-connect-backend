// Package wajub talks to Wajub (https://docs.wajub.com): mobile money
// payments pushed to the members' phones (they confirm with their PIN), card
// payments on Wajub's hosted page, refunds, sendings to an organizer, the
// balance. One address for the sandbox and production: the key tells
// (sk_test.… or sk.…). Webhooks are signed: [VerifySignature].
package wajub

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
	"strconv"
	"strings"
	"time"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/money"
)

const DefaultURL = "https://api.wajub.com"

// Channels: Wajub's channel of each operator the app offers.
var Channels = map[string]string{
	"MTN_MOMO_CMR": "cm.mtn",
	"ORANGE_CMR":   "cm.orange",
}

// Client of the API, with the secret key of the account; [Fees] are the
// shares Wajub keeps (its API doesn't give them).
type Client struct {
	BaseURL   string
	SecretKey string
	Fees      money.Fees
	HTTP      *http.Client
}

func New(baseURL, secretKey string, fees money.Fees) *Client {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	return &Client{
		BaseURL:   strings.TrimSuffix(baseURL, "/"),
		SecretKey: secretKey,
		Fees:      fees,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

// transaction: a payment as Wajub writes it (under "transaction").
type transaction struct {
	ID               string  `json:"id"`
	Reference        string  `json:"reference"`
	Status           string  `json:"status"`
	Amount           float64 `json:"amount"`
	AmountPaid       float64 `json:"amount_paid"`
	AuthorizationURL string  `json:"authorization_url"`
	FailureReason    string  `json:"failure_reason"`
	FailureMessage   string  `json:"failure_message"`
}

// Init asks for a payment: a mobile money one is created then charged on the
// operator's channel, which pushes the request to the phone; a card one opens
// Wajub's page ([money.Payment.GatewayURL]). Our id is its reference, and
// the key that makes a retry harmless.
func (c *Client) Init(ctx context.Context, req money.InitRequest) (money.Payment, error) {
	body := map[string]any{
		"amount":      req.Amount,
		"currency":    req.Currency,
		"description": req.Description,
		"reference":   req.ExternalID,
		"metadata":    req.Metadata,
	}
	card := req.PaymentMethod == money.CardPayment
	channel := ""
	if card {
		if req.Email != "" {
			body["customer"] = map[string]any{"email": req.Email}
		}
		body["accept"] = map[string]any{"channels": []string{"card"}}
		if req.ReturnURL != "" {
			// Wajub adds ?reference=…&status=…&trxref=… to it
			body["callback"] = req.ReturnURL
		}
	} else {
		var ok bool
		if channel, ok = Channels[req.Provider]; !ok {
			return money.Payment{}, &money.RefusedError{Status: http.StatusConflict, Message: "provider not offered: " + req.Provider}
		}
		body["customer"] = map[string]any{"phone": international(req.PhoneNumber)}
		body["accept"] = map[string]any{"channels": []string{channel}}
		// the member has half an hour to confirm
		body["expires"] = map[string]any{"in": 30}
	}
	var created struct {
		// the hosted page (a card)
		AuthorizationURL string      `json:"authorization_url"`
		Transaction      transaction `json:"transaction"`
	}
	if err := c.call(ctx, http.MethodPost, "/payments", req.ExternalID+":create", body, &created); err != nil {
		return money.Payment{}, err
	}
	payment := money.Payment{ID: created.Transaction.ID, Reference: req.ExternalID, Status: status(created.Transaction.Status)}
	if card {
		payment.GatewayURL = created.AuthorizationURL
		if payment.GatewayURL == "" {
			payment.GatewayURL = created.Transaction.AuthorizationURL
		}
		return payment, nil
	}
	var charged struct {
		Transaction transaction `json:"transaction"`
	}
	charge := map[string]any{"channel": channel, "data": map[string]any{"phone": international(req.PhoneNumber)}}
	if err := c.call(ctx, http.MethodPost, "/payments/"+url.PathEscape(payment.ID), req.ExternalID+":charge", charge, &charged); err != nil {
		return payment, err
	}
	if charged.Transaction.Status != "" {
		payment.Status = status(charged.Transaction.Status)
	}
	return payment, nil
}

// Lookup: where payment [id] (Wajub's) is, read from Wajub: a webhook that
// may never have come.
func (c *Client) Lookup(ctx context.Context, id string) (money.Webhook, error) {
	var answer struct {
		Transaction transaction `json:"transaction"`
	}
	if err := c.call(ctx, http.MethodGet, "/payments/"+url.PathEscape(id), "", nil, &answer); err != nil {
		return money.Webhook{}, err
	}
	return paymentHook("payment."+answer.Transaction.Status, answer.Transaction), nil
}

// Refund gives the whole payment [paymentID] (Wajub's id) back to its payer.
func (c *Client) Refund(ctx context.Context, paymentID string, req money.RefundRequest) (money.Refund, error) {
	reason := "requested_by_customer"
	if strings.Contains(req.Reason, "unavailable") {
		reason = "service_not_delivered"
	}
	var answer struct {
		Refund struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"refund"`
	}
	err := c.call(ctx, http.MethodPost, "/refunds", req.ExternalID, map[string]any{
		"payment": paymentID, "reason": reason, "reference": req.ExternalID,
	}, &answer)
	return money.Refund{ID: answer.Refund.ID, Status: status(answer.Refund.Status)}, err
}

// Withdraw sends money from the balance to a mobile money account:
// [money.ErrBalanceTooLow] when the balance can't cover it.
func (c *Client) Withdraw(ctx context.Context, req money.WithdrawRequest) (money.Withdrawal, error) {
	channel, ok := Channels[req.Provider]
	if !ok {
		return money.Withdrawal{}, &money.RefusedError{Status: http.StatusConflict, Message: "provider not offered: " + req.Provider}
	}
	currency := req.Currency
	if currency == "" {
		currency = "XAF"
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "CMFI Connect"
	}
	var answer struct {
		Transfer struct {
			ID     string  `json:"id"`
			Status string  `json:"status"`
			Amount float64 `json:"amount"`
		} `json:"transfer"`
	}
	err := c.call(ctx, http.MethodPost, "/transfers", req.ExternalID, map[string]any{
		"amount":      req.Amount,
		"currency":    currency,
		"description": req.Description,
		"reference":   req.ExternalID,
		"beneficiary": map[string]any{"name": name, "channel": channel, "phone": international(req.PhoneNumber)},
	}, &answer)
	if refused := (*money.RefusedError)(nil); errors.As(err, &refused) && refused.Status == http.StatusUnprocessableEntity &&
		(strings.Contains(strings.ToLower(refused.Message), "balance") || strings.Contains(strings.ToLower(refused.Message), "insufficient")) {
		return money.Withdrawal{}, money.ErrBalanceTooLow
	}
	return money.Withdrawal{ID: answer.Transfer.ID, Status: status(answer.Transfer.Status), NetAmount: answer.Transfer.Amount}, err
}

// Balances of the account, in each currency.
func (c *Client) Balances(ctx context.Context) ([]money.Balance, error) {
	var answer struct {
		Balance json.RawMessage `json:"balance"`
	}
	if err := c.call(ctx, http.MethodGet, "/balance", "", nil, &answer); err != nil {
		return nil, err
	}
	type one struct {
		Currency  string  `json:"currency"`
		Available float64 `json:"available"`
		Pending   float64 `json:"pending"`
		Reserved  float64 `json:"reserved"`
		Total     float64 `json:"total"`
	}
	var many []one
	if err := json.Unmarshal(answer.Balance, &many); err != nil {
		var single one
		if err := json.Unmarshal(answer.Balance, &single); err != nil {
			return nil, fmt.Errorf("wajub: unreadable balance: %w", err)
		}
		many = []one{single}
	}
	balances := []money.Balance{}
	for _, b := range many {
		reserved := b.Reserved + b.Pending
		total := b.Total
		if total == 0 {
			total = b.Available + reserved
		}
		balances = append(balances, money.Balance{
			Currency: strings.ToUpper(b.Currency), Balance: total, ReservedBalance: reserved, AvailableBalance: b.Available,
		})
	}
	return balances, nil
}

// PlatformFees: the shares Wajub keeps, as configured.
func (c *Client) PlatformFees(context.Context) (money.Fees, error) {
	return c.Fees, nil
}

// PredictProvider: the operator of a Cameroon number, read from its prefix
// (Wajub has no such call), and the number as kept (237670000000). The
// member may still choose the other operator.
func (c *Client) PredictProvider(_ context.Context, phoneNumber string) (money.Prediction, error) {
	digits := digitsOf(phoneNumber)
	digits = strings.TrimPrefix(digits, "00")
	if len(digits) == 9 {
		digits = "237" + digits
	}
	if len(digits) != 12 || !strings.HasPrefix(digits, "2376") {
		return money.Prediction{}, money.ErrInvalidPhone
	}
	local := digits[3:]
	provider := ""
	switch {
	case strings.HasPrefix(local, "67"), inRange(local, 650, 654), inRange(local, 680, 684):
		provider = "MTN_MOMO_CMR"
	case strings.HasPrefix(local, "69"), inRange(local, 655, 659), inRange(local, 685, 689):
		provider = "ORANGE_CMR"
	default:
		return money.Prediction{}, money.ErrInvalidPhone
	}
	return money.Prediction{Country: "CMR", Provider: provider, PhoneNumber: digits}, nil
}

func inRange(local string, low, high int) bool {
	n, err := strconv.Atoi(local[:3])
	return err == nil && n >= low && n <= high
}

func digitsOf(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// international: "237670000000" -> "+237670000000", as Wajub wants it.
func international(phone string) string {
	digits := strings.TrimPrefix(digitsOf(phone), "00")
	if len(digits) == 9 {
		digits = "237" + digits
	}
	return "+" + digits
}

// status: Wajub's status in the API's words.
func status(s string) string {
	switch s {
	case "succeeded":
		return money.Completed
	case "failed", "expired":
		return money.Failed
	case "cancelled":
		return money.Cancelled
	case "processing", "review":
		return money.Processing
	default:
		return money.Pending
	}
}

func (c *Client) call(ctx context.Context, method, path, idempotency string, body, answer any) error {
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
	// the key itself, without "Bearer" (docs.wajub.com, quickstart)
	req.Header.Set("Authorization", c.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if idempotency != "" {
		req.Header.Set("Idempotency-Key", idempotency)
	}
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
	case resp.StatusCode >= 300:
		var refused struct {
			Message any `json:"message"`
		}
		_ = json.Unmarshal(data, &refused)
		return &money.RefusedError{Status: resp.StatusCode, Message: fmt.Sprint(refused.Message)}
	}
	if answer == nil {
		return nil
	}
	if err := json.Unmarshal(data, answer); err != nil {
		return fmt.Errorf("wajub: unreadable answer: %w", err)
	}
	return nil
}

// ----- webhooks -----

// tolerance: how far the signed time may be from ours.
const tolerance = 5 * time.Minute

// VerifySignature: [signature] (X-Wajub-Signature) is "v1=" and the hex
// HMAC-SHA256 of "[timestamp].[body]" (X-Wajub-Timestamp, Unix seconds;
// the raw body) with the endpoint's [secret], the timestamp within 5 minutes
// of [now].
func VerifySignature(body []byte, signature, timestamp, secret string, now time.Time) bool {
	sent, ok := strings.CutPrefix(strings.TrimSpace(signature), "v1=")
	if secret == "" || !ok {
		return false
	}
	given, err := hex.DecodeString(sent)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strings.TrimSpace(timestamp) + "."))
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), given) {
		return false
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return false
	}
	gap := now.Sub(time.Unix(seconds, 0))
	return gap <= tolerance && gap >= -tolerance
}

// Sign gives the headers Wajub would send for [body] at [now] (for tests).
func Sign(body []byte, secret string, now time.Time) (signature, timestamp string) {
	timestamp = strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil)), timestamp
}

// ParseWebhook reads a delivery (its signature already checked) in the
// API's words: payment.succeeded is payment.completed, transfer.* is
// payout.*, an expired payment a failed one.
func ParseWebhook(body []byte) (money.Webhook, error) {
	var envelope struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return money.Webhook{}, err
	}
	kind, outcome, _ := strings.Cut(envelope.Event, ".")
	switch kind {
	case "payment":
		var data transaction
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return money.Webhook{}, err
		}
		return paymentHook(envelope.Event, data), nil
	case "refund", "transfer":
		var data struct {
			ID             string  `json:"id"`
			Reference      string  `json:"reference"`
			Status         string  `json:"status"`
			Amount         float64 `json:"amount"`
			FailureReason  string  `json:"failure_reason"`
			FailureMessage string  `json:"failure_message"`
		}
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return money.Webhook{}, err
		}
		name := kind
		if kind == "transfer" {
			name = "payout"
		}
		hook := money.Webhook{
			Event: name + "." + outcome, PaymentID: data.ID, ExternalID: data.Reference,
			Status: status(data.Status), Amount: data.Amount, FailureReason: failureOf(data.FailureReason, data.FailureMessage),
		}
		if outcome == "succeeded" {
			hook.Event, hook.Status = name+".completed", money.Completed
		}
		return hook, nil
	default:
		return money.Webhook{Event: envelope.Event}, nil
	}
}

// paymentHook: a payment's [event] (payment.succeeded…) in the API's words.
func paymentHook(event string, data transaction) money.Webhook {
	paid := data.AmountPaid
	if paid == 0 {
		paid = data.Amount
	}
	hook := money.Webhook{
		Event: event, PaymentID: data.ID, Reference: data.ID, ExternalID: data.Reference,
		Status: status(data.Status), Amount: paid,
	}
	switch event {
	case "payment.succeeded":
		hook.Event, hook.Status = "payment.completed", money.Completed
	case "payment.failed":
		hook.Status = money.Failed
		hook.FailureCode = codeOf(data.FailureReason)
		hook.FailureReason = failureOf(data.FailureReason, data.FailureMessage)
	case "payment.expired":
		hook.Event, hook.Status, hook.FailureCode = "payment.failed", money.Failed, "EXPIRED"
	case "payment.cancelled":
		hook.Status = money.Cancelled
	}
	return hook
}

// codeOf: Wajub's failure reason ("insufficient_funds") as a code the apps
// translate ("INSUFFICIENT_FUNDS"); "" when none.
func codeOf(reason string) string {
	return strings.ToUpper(strings.TrimSpace(reason))
}

func failureOf(reason, message string) string {
	if reason != "" {
		return codeOf(reason)
	}
	return message
}
