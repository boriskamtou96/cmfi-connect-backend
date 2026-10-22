package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/kpay"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The money of an event goes from the KPAY wallet to the event's own mobile
// money account when an admin asks (« Reverser »): one sending for what was
// paid and not sent yet, net of KPAY's commission on the payments. The
// account is confirmed by a test of 100 FCFA first: only a confirmed account
// receives the money. KPAY tells the outcome by its signed webhook
// (payout.completed, payout.failed).

const (
	payoutTest     = "test"
	payoutTransfer = "transfer"

	// the test that confirms an account, and KPAY's limits for a withdrawal
	payoutTestAmount = 100
	payoutMin        = 100
	payoutMax        = 500_000
)

// feesCache: KPAY's commissions, asked every hour at most (5 % when it can't
// be asked).
type feesCache struct {
	mu      sync.Mutex
	fees    kpay.Fees
	fetched time.Time
}

var defaultFees = kpay.Fees{PaymentRate: 0.05, WithdrawalRate: 0.05}

func (s *Server) kpayFees(ctx context.Context) kpay.Fees {
	s.fees.mu.Lock()
	defer s.fees.mu.Unlock()
	if !s.fees.fetched.IsZero() && time.Since(s.fees.fetched) < time.Hour {
		return s.fees.fees
	}
	fees, err := s.money.PlatformFees(ctx)
	if err != nil || fees.PaymentRate <= 0 || fees.PaymentRate >= 1 {
		slog.Warn("kpay fees unknown: 5 % assumed", "error", err)
		return defaultFees
	}
	s.fees.fees, s.fees.fetched = fees, time.Now()
	return fees
}

// netOf: what a payment of [amount] leaves in the wallet.
func netOf(amount int64, rate float64) int64 {
	return amount - int64(math.Round(float64(amount)*rate))
}

type payoutResponse struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Amount      int64      `json:"amount"`
	Currency    string     `json:"currency"`
	PhoneNumber string     `json:"phone_number"`
	Provider    string     `json:"provider"`
	Holder      string     `json:"holder"`
	Status      string     `json:"status"`
	FailureCode string     `json:"failure_code"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at"`
	// what reaches the account, KPAY's commission on the withdrawal taken
	Received int64 `json:"received"`
}

type payoutAccountResponse struct {
	PhoneNumber string     `json:"phone_number"`
	Provider    string     `json:"provider"`
	Holder      string     `json:"holder"`
	ConfirmedAt *time.Time `json:"confirmed_at"`
	// the last test of 100 FCFA, null before
	Test *payoutResponse `json:"test"`
}

type toForwardResponse struct {
	Payments int    `json:"payments"`
	Amount   int64  `json:"amount"`
	Net      int64  `json:"net"`
	Currency string `json:"currency"`
}

// payoutsOverview: the event's account, its money still in the wallet, and
// its sendings.
type payoutsOverview struct {
	Account   *payoutAccountResponse `json:"account"`
	ToForward toForwardResponse      `json:"to_forward"`
	FeeRates  kpay.Fees              `json:"fee_rates"`
	Payouts   []payoutResponse       `json:"payouts"`
}

func toPayoutResponse(payout db.Payout, fees kpay.Fees) payoutResponse {
	return payoutResponse{
		ID: payout.ID.String(), Kind: payout.Kind, Amount: payout.Amount, Currency: payout.Currency,
		PhoneNumber: payout.PhoneNumber, Provider: payout.Provider, Holder: payout.Holder,
		Status: payout.Status, FailureCode: payout.FailureCode, CreatedAt: payout.CreatedAt,
		CompletedAt: timePtr(payout.CompletedAt),
		Received:    netOf(payout.Amount, fees.WithdrawalRate),
	}
}

func (s *Server) payoutsOverview(c *gin.Context, event db.Event) (payoutsOverview, error) {
	fees := s.kpayFees(c)
	overview := payoutsOverview{FeeRates: fees, Payouts: []payoutResponse{}}
	payouts, err := s.store.ListEventPayouts(c, nullID(event.ID))
	if err != nil {
		return overview, err
	}
	if event.PayoutPhone != "" {
		overview.Account = &payoutAccountResponse{
			PhoneNumber: event.PayoutPhone, Provider: event.PayoutProvider, Holder: event.PayoutHolder,
			ConfirmedAt: timePtr(event.PayoutConfirmedAt),
		}
	}
	for _, payout := range payouts {
		response := toPayoutResponse(payout, fees)
		if payout.Kind == payoutTest {
			if overview.Account != nil && overview.Account.Test == nil &&
				payout.PhoneNumber == event.PayoutPhone && payout.Provider == event.PayoutProvider {
				overview.Account.Test = &response
			}
			continue
		}
		overview.Payouts = append(overview.Payouts, response)
	}
	payments, err := s.store.ListPaymentsToForward(c, nullID(event.ID))
	if err != nil {
		return overview, err
	}
	overview.ToForward = toForwardResponse{Payments: len(payments), Currency: event.PriceCurrency}
	for _, payment := range payments {
		overview.ToForward.Amount += payment.Amount
		overview.ToForward.Net += netOf(payment.Amount, fees.PaymentRate)
	}
	return overview, nil
}

func (s *Server) sendPayoutsOverview(c *gin.Context, event db.Event) {
	overview, err := s.payoutsOverview(c, event)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, overview)
}

// GET /v1/admin/events/:id/payouts
func (s *Server) adminEventPayouts(c *gin.Context) {
	if !s.paymentsEnabled(c) {
		return
	}
	event, ok := s.loadEvent(c, true)
	if !ok {
		return
	}
	s.sendPayoutsOverview(c, event)
}

type payoutAccountRequest struct {
	// with the country code: +237 6 99 00 00 00
	PhoneNumber string `json:"phone_number" binding:"required"`
	// ORANGE_CMR, MTN_MOMO_CMR; the operator of the number when empty
	Provider string `json:"provider"`
	// the name of the account's holder, as the operator knows it
	Holder string `json:"holder" binding:"required"`
}

// PUT /v1/admin/events/:id/payout-account: the account that receives the
// event's money; 100 FCFA are sent to it at once, and it is confirmed when
// they arrive.
func (s *Server) setEventPayoutAccount(c *gin.Context) {
	if !s.paymentsEnabled(c) {
		return
	}
	event, ok := s.loadEvent(c, true)
	if !ok {
		return
	}
	var req payoutAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	holder := strings.TrimSpace(req.Holder)
	if holder == "" {
		badRequestError(c, fmt.Errorf("holder is required"))
		return
	}
	prediction, err := s.money.PredictProvider(c, req.PhoneNumber)
	if errors.Is(err, kpay.ErrInvalidPhone) {
		c.JSON(http.StatusBadRequest, NewErrorResponse(http.StatusBadRequest, "INVALID_PHONE", err))
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = prediction.Provider
	}
	if code, err := checkOperator(s.operators(), provider, event.PriceCurrency, payoutTestAmount); err != nil {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, code, err))
		return
	}
	event, err = s.store.SetEventPayoutAccount(c, db.SetEventPayoutAccountParams{
		ID: event.ID, PayoutPhone: prediction.PhoneNumber, PayoutProvider: provider, PayoutHolder: holder,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	logFor(c).Info("event payout account set", "event", event.ID, "provider", provider)
	s.sendPayoutTest(c, event)
	s.sendPayoutsOverview(c, event)
}

// POST /v1/admin/events/:id/payout-account/test: the test again (it failed,
// or never arrived).
func (s *Server) retestEventPayoutAccount(c *gin.Context) {
	if !s.paymentsEnabled(c) {
		return
	}
	event, ok := s.loadEvent(c, true)
	if !ok {
		return
	}
	if event.PayoutPhone == "" {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "NO_PAYOUT_ACCOUNT", fmt.Errorf("no account set")))
		return
	}
	s.sendPayoutTest(c, event)
	s.sendPayoutsOverview(c, event)
}

func (s *Server) sendPayoutTest(c *gin.Context, event db.Event) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	payout, err := s.store.CreatePayout(c, db.CreatePayoutParams{
		ID: uuid.New(), EventID: nullID(event.ID), Kind: payoutTest, Amount: payoutTestAmount,
		Currency: event.PriceCurrency, PhoneNumber: event.PayoutPhone, Provider: event.PayoutProvider,
		Holder: event.PayoutHolder, CreatedBy: nullID(authUser.ID),
	})
	if err != nil {
		slog.Error("cannot keep a payout test", "event", event.ID, "error", err)
		return
	}
	s.sendPayout(c, payout, "CMFI Connect · test du compte · "+event.Title)
}

// POST /v1/admin/events/:id/payouts: sends what was paid for the event and
// not sent yet (net of KPAY's commission) to its confirmed account, 500 000
// at most at a time.
func (s *Server) forwardEventMoney(c *gin.Context) {
	if !s.paymentsEnabled(c) {
		return
	}
	event, ok := s.loadEvent(c, true)
	if !ok {
		return
	}
	if event.PayoutPhone == "" || !event.PayoutConfirmedAt.Valid {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "PAYOUT_ACCOUNT_NOT_CONFIRMED",
			fmt.Errorf("the event's account is not confirmed yet")))
		return
	}
	payments, err := s.store.ListPaymentsToForward(c, nullID(event.ID))
	if err != nil {
		internalServerError(c, err)
		return
	}
	fees := s.kpayFees(c)
	var amount int64
	ids := []uuid.UUID{}
	for _, payment := range payments {
		net := netOf(payment.Amount, fees.PaymentRate)
		if amount+net > payoutMax {
			break // the rest goes with the next sending
		}
		amount += net
		ids = append(ids, payment.ID)
	}
	if amount < payoutMin {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "NOTHING_TO_FORWARD",
			fmt.Errorf("%d %s to forward: %d at least", amount, event.PriceCurrency, payoutMin)))
		return
	}
	if balances, err := s.money.Balances(c); err == nil {
		available := 0.0
		for _, balance := range balances {
			if balance.Currency == event.PriceCurrency {
				available = balance.AvailableBalance
			}
		}
		if available < float64(amount) {
			c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "WALLET_BALANCE_TOO_LOW",
				fmt.Errorf("%d %s to send, %.0f available in the wallet", amount, event.PriceCurrency, available)))
			return
		}
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	payout, err := s.store.CreatePayout(c, db.CreatePayoutParams{
		ID: uuid.New(), EventID: nullID(event.ID), Kind: payoutTransfer, Amount: amount,
		Currency: event.PriceCurrency, PhoneNumber: event.PayoutPhone, Provider: event.PayoutProvider,
		Holder: event.PayoutHolder, CreatedBy: nullID(authUser.ID),
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if err := s.store.LinkPaymentsToPayout(c, db.LinkPaymentsToPayoutParams{
		PayoutID: uuid.NullUUID{UUID: payout.ID, Valid: true}, Ids: ids,
	}); err != nil {
		internalServerError(c, err)
		return
	}
	logFor(c).Info("event money forwarded", "event", event.ID, "payout", payout.ID, "payments", len(ids), "amount", amount)
	s.sendPayout(c, payout, "CMFI Connect · "+event.Title)
	s.sendPayoutsOverview(c, event)
}

// sendPayout asks KPAY for the sending.
func (s *Server) sendPayout(ctx context.Context, payout db.Payout, description string) {
	sent, err := s.money.Withdraw(ctx, kpay.WithdrawRequest{
		Amount: payout.Amount, Provider: payout.Provider, PhoneNumber: payout.PhoneNumber,
		ExternalID: payout.ID.String(), Description: description,
	})
	var refused *kpay.RefusedError
	switch {
	case errors.Is(err, kpay.ErrUnknown):
		slog.Warn("payout outcome unknown", "payout", payout.ID, "error", err)
	case errors.Is(err, kpay.ErrBalanceTooLow):
		s.failPayout(ctx, payout.ID, "WALLET_BALANCE_TOO_LOW")
	case errors.As(err, &refused):
		slog.Warn("payout refused by kpay", "payout", payout.ID, "status", refused.Status, "reason", refused.Message)
		code := refusalCode(refused)
		if code == "REJECTED" {
			code = "PAYOUT_REFUSED"
		}
		s.failPayout(ctx, payout.ID, code)
	case err != nil:
		slog.Error("payout not sent to kpay", "payout", payout.ID, "error", err)
		s.failPayout(ctx, payout.ID, "PAYMENTS_UNAVAILABLE")
	case sent.Status == kpay.Failed || sent.Status == kpay.Cancelled:
		s.failPayout(ctx, payout.ID, "UNSPECIFIED_FAILURE")
	default:
		if err := s.store.MarkPayoutPending(ctx, db.MarkPayoutPendingParams{ID: payout.ID, ProviderPayoutID: sent.ID}); err != nil {
			slog.Error("cannot mark a payout pending", "payout", payout.ID, "error", err)
		}
	}
}

// failPayout: a failed transfer gives its payments back to the next one.
func (s *Server) failPayout(ctx context.Context, id uuid.UUID, code string) {
	payout, err := s.store.FailPayout(ctx, db.FailPayoutParams{ID: id, FailureCode: code})
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		slog.Error("cannot mark a payout failed", "payout", id, "error", err)
		return
	}
	slog.Info("payout failed", "payout", id, "kind", payout.Kind, "code", code)
	if payout.Kind == payoutTransfer {
		if err := s.store.UnlinkPayoutPayments(ctx, uuid.NullUUID{UUID: id, Valid: true}); err != nil {
			slog.Error("cannot free the payments of a failed payout", "payout", id, "error", err)
		}
	}
}

// onPayoutWebhook: KPAY says how a sending ended. A test that arrived
// confirms the account it went to.
func (s *Server) onPayoutWebhook(ctx context.Context, hook kpay.Webhook) {
	var payout db.Payout
	found := false
	if id, err := uuid.Parse(hook.ExternalID); err == nil {
		payout, err = s.store.GetPayout(ctx, id)
		found = err == nil
	}
	if !found && hook.PaymentID != "" {
		var err error
		payout, err = s.store.GetPayoutByProviderID(ctx, hook.PaymentID)
		found = err == nil
	}
	if !found {
		slog.Warn("kpay webhook for an unknown payout", "event", hook.Event)
		return
	}
	switch {
	case hook.Event == "payout.completed" || hook.Status == kpay.Completed:
		completed, err := s.store.CompletePayout(ctx, payout.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			slog.Error("cannot mark a payout completed", "payout", payout.ID, "error", err)
			return
		}
		slog.Info("payout completed", "payout", payout.ID, "kind", payout.Kind)
		if completed.Kind == payoutTest && completed.EventID.Valid {
			if err := s.store.ConfirmEventPayoutAccount(ctx, db.ConfirmEventPayoutAccountParams{
				ID: completed.EventID.Int64, PayoutPhone: completed.PhoneNumber, PayoutProvider: completed.Provider,
			}); err != nil {
				slog.Error("cannot confirm a payout account", "payout", payout.ID, "error", err)
			}
		}
	case hook.Event == "payout.failed" || hook.Event == "payout.cancelled" ||
		hook.Status == kpay.Failed || hook.Status == kpay.Cancelled:
		s.failPayout(ctx, payout.ID, webhookFailure(hook))
	}
}
