package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/kpay"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Payments by mobile money, through KPAY-NOW (internal/kpay): the
// registration to a paid event (from the app) and Premium (from the website;
// the stores forbid it in the apps). The member confirms on their phone with
// their PIN; KPAY tells the outcome by a webhook, believed only when signed
// with the webhook secret (KPAY can't be asked about a payment). Our id is
// KPAY's externalId. A reconciler finishes what stays undone (the work of a
// completed payment, a refund that couldn't be sent). The amount is always
// computed here.

// MobileMoney is KPAY (a fake in the tests).
type MobileMoney interface {
	Init(ctx context.Context, req kpay.InitRequest) (kpay.Payment, error)
	Refund(ctx context.Context, paymentID string, req kpay.RefundRequest) (kpay.Refund, error)
	PredictProvider(ctx context.Context, phoneNumber string) (kpay.Prediction, error)
	Withdraw(ctx context.Context, req kpay.WithdrawRequest) (kpay.Withdrawal, error)
	Balances(ctx context.Context) ([]kpay.Balance, error)
	PlatformFees(ctx context.Context) (kpay.Fees, error)
}

// PremiumGranter gives Premium in RevenueCat (internal/revenuecat), seen by
// the apps like a purchase.
type PremiumGranter interface {
	Grant(ctx context.Context, appUserID string, until *time.Time) error
	Revoke(ctx context.Context, appUserID string) error
}

const (
	paymentForEvent   = "event"
	paymentForPremium = "premium"

	// what the member sees with the payment
	paymentStatementText = "CMFI Connect"

	refundByAdmin          = "admin"
	refundEventUnavailable = "event_unavailable"
)

// The Premium plans sold on the website, in the order shown.
var premiumPlans = []string{"monthly", "six_month", "yearly", "lifetime"}

// premiumEnd: the end of [plan] bought at [from]; nil for life.
func premiumEnd(plan string, from time.Time) *time.Time {
	var end time.Time
	switch plan {
	case "monthly":
		end = from.AddDate(0, 1, 0)
	case "six_month":
		end = from.AddDate(0, 6, 0)
	case "yearly":
		end = from.AddDate(1, 0, 0)
	default:
		return nil
	}
	return &end
}

// paymentsEnabled answers 503 PAYMENTS_DISABLED without KPAY.
func (s *Server) paymentsEnabled(c *gin.Context) bool {
	if s.money == nil {
		c.JSON(http.StatusServiceUnavailable, NewErrorResponse(http.StatusServiceUnavailable,
			"PAYMENTS_DISABLED", fmt.Errorf("payments are not available")))
		return false
	}
	return true
}

// ----- the answers -----

// paymentResponse: a payment as its member sees it. Status is pending (the
// member confirms on their phone, or it is being finished), completed,
// failed, refunding or refunded.
type paymentResponse struct {
	ID          string    `json:"id"`
	Purpose     string    `json:"purpose"`
	Status      string    `json:"status"`
	FailureCode string    `json:"failure_code"`
	Amount      int64     `json:"amount"`
	Currency    string    `json:"currency"`
	Provider    string    `json:"provider"`
	EventID     *int64    `json:"event_id"`
	People      int32     `json:"people"`
	PremiumPlan string    `json:"premium_plan"`
	CreatedAt   time.Time `json:"created_at"`
	// a card: KPAY's page to open, given when the payment starts
	GatewayURL string `json:"gateway_url,omitempty"`
	// the end of the Premium it gave; null before, or for life
	PremiumUntil *time.Time `json:"premium_until"`
}

func memberStatus(payment db.Payment) string {
	switch payment.Status {
	case "completed":
		if payment.SettledAt.Valid {
			return "completed"
		}
		return "pending"
	case "failed":
		return "failed"
	case "refund_pending", "refund_failed":
		return "refunding"
	case "refunded":
		return "refunded"
	default:
		return "pending"
	}
}

func toPaymentResponse(payment db.Payment) paymentResponse {
	return paymentResponse{
		ID:           payment.ID.String(),
		Purpose:      payment.Purpose,
		Status:       memberStatus(payment),
		FailureCode:  payment.FailureCode,
		Amount:       payment.Amount,
		Currency:     payment.Currency,
		Provider:     payment.Provider,
		EventID:      idPtr(payment.EventID),
		People:       payment.People,
		PremiumPlan:  payment.PremiumPlan,
		CreatedAt:    payment.CreatedAt,
		PremiumUntil: timePtr(payment.PremiumUntil),
	}
}

// ----- the operators -----

// defaultProviders: the operators offered without KPAY_PROVIDERS.
var defaultProviders = []string{"MTN_MOMO_CMR", "ORANGE_CMR"}

// operators: those of KPAY's catalogue the application offers.
func (s *Server) operators() []kpay.Operator {
	allowed := s.config.Pay.KPayProviders
	if len(allowed) == 0 {
		allowed = defaultProviders
	}
	found := []kpay.Operator{}
	for _, operator := range kpay.Operators {
		if slices.Contains(allowed, operator.Provider) {
			found = append(found, operator)
		}
	}
	return found
}

type providerResponse struct {
	Provider  string `json:"provider"`
	Name      string `json:"name"`
	Logo      string `json:"logo"`
	MinAmount string `json:"min_amount"`
	MaxAmount string `json:"max_amount"`
	// OPERATIONAL (KPAY gives no other state here)
	Status string `json:"status"`
}

type countryResponse struct {
	Country   string             `json:"country"`
	Names     map[string]string  `json:"names"`
	Prefix    string             `json:"prefix"`
	Flag      string             `json:"flag"`
	Providers []providerResponse `json:"providers"`
}

// operatorsFor: the countries and operators that take [currency].
func operatorsFor(operators []kpay.Operator, currency string) []countryResponse {
	countries := []countryResponse{}
	index := map[string]int{}
	for _, operator := range operators {
		if operator.Currency != currency {
			continue
		}
		i, ok := index[operator.Country]
		if !ok {
			i = len(countries)
			index[operator.Country] = i
			countries = append(countries, countryResponse{
				Country: operator.Country, Names: operator.Names, Prefix: operator.Prefix,
				Providers: []providerResponse{},
			})
		}
		countries[i].Providers = append(countries[i].Providers, providerResponse{
			Provider: operator.Provider, Name: operator.Name, MinAmount: operator.MinAmount, Status: "OPERATIONAL",
		})
	}
	return countries
}

// GET /v1/payments/providers?currency=XAF: where one can pay from.
func (s *Server) listPaymentProviders(c *gin.Context) {
	if !s.paymentsEnabled(c) {
		return
	}
	currency := strings.ToUpper(c.DefaultQuery("currency", "XAF"))
	apiResponse(c, http.StatusOK, gin.H{
		"countries": operatorsFor(s.operators(), currency),
		// Visa and Mastercard on KPAY's page
		"cards": s.config.Pay.KPayCards,
	})
}

type predictRequest struct {
	PhoneNumber string `json:"phone_number" binding:"required"`
}

// POST /v1/payments/predict-provider: the operator of a number, and the
// number as KPAY writes it.
func (s *Server) predictPaymentProvider(c *gin.Context) {
	if !s.paymentsEnabled(c) {
		return
	}
	var req predictRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
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
	apiResponse(c, http.StatusOK, gin.H{
		"country": prediction.Country, "provider": prediction.Provider, "phone_number": prediction.PhoneNumber,
	})
}

// ----- starting a payment -----

type payerRequest struct {
	// mobile_money (by default) or card (KPAY's hosted page)
	Method string `json:"method"`
	// mobile money, with the country code: +237 6 70 00 00 00
	PhoneNumber string `json:"phone_number"`
	// MTN_MOMO_CMR, ORANGE_CMR…; the operator of the number when empty
	Provider string `json:"provider"`
	// a card: where the payer comes back (a page of an allowed site), the
	// page of the site that says to go back to the app by default
	ReturnURL string `json:"return_url"`
}

const (
	methodMobileMoney = "mobile_money"
	methodCard        = "card"
	// the provider written on a payment by card
	cardProvider = "CARD"
)

// returnURL: where the payer of [id] comes back after the card page: the
// address asked when it is on an allowed site (CORS_ORIGINS, the site of
// PAYMENT_RETURN_URL), else PAYMENT_RETURN_URL; with ?payment=<id>.
func (s *Server) returnURL(asked string, id uuid.UUID) string {
	fallback, _ := url.Parse(s.config.Pay.ReturnURL)
	chosen := fallback
	if parsed, err := url.Parse(strings.TrimSpace(asked)); err == nil && parsed.Scheme == "https" && parsed.Host != "" {
		origin := parsed.Scheme + "://" + parsed.Host
		allowed := fallback != nil && origin == fallback.Scheme+"://"+fallback.Host
		for _, site := range s.config.Server.CORSOrigins {
			allowed = allowed || strings.EqualFold(strings.TrimSuffix(site, "/"), origin)
		}
		if allowed {
			chosen = parsed
		}
	}
	if chosen == nil {
		return ""
	}
	query := chosen.Query()
	query.Set("payment", id.String())
	result := *chosen
	result.RawQuery = query.Encode()
	return result.String()
}

// startPayment checks the payer, keeps the payment, then asks KPAY. It
// answers the payment (201), failed when refused, or the error.
func (s *Server) startPayment(c *gin.Context, params db.CreatePaymentParams, payer payerRequest, description string) {
	switch payer.Method {
	case methodCard:
		if !s.config.Pay.KPayCards {
			c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "CARDS_DISABLED", fmt.Errorf("cards are not taken")))
			return
		}
		s.startCardPayment(c, params, payer, description)
		return
	case "", methodMobileMoney:
	default:
		badRequestError(c, fmt.Errorf("method: mobile_money or card"))
		return
	}
	if strings.TrimSpace(payer.PhoneNumber) == "" {
		badRequestError(c, fmt.Errorf("phone_number is required"))
		return
	}
	prediction, err := s.money.PredictProvider(c, payer.PhoneNumber)
	if errors.Is(err, kpay.ErrInvalidPhone) {
		c.JSON(http.StatusBadRequest, NewErrorResponse(http.StatusBadRequest, "INVALID_PHONE", err))
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	params.PhoneNumber = prediction.PhoneNumber
	params.Provider = strings.TrimSpace(payer.Provider)
	if params.Provider == "" {
		params.Provider = prediction.Provider
	}
	if code, err := checkOperator(s.operators(), params.Provider, params.Currency, params.Amount); err != nil {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, code, err))
		return
	}

	params.ID = uuid.New()
	payment, err := s.store.CreatePayment(c, params)
	if err != nil {
		internalServerError(c, err)
		return
	}
	started, err := s.money.Init(c, kpay.InitRequest{
		Amount:      payment.Amount,
		Currency:    payment.Currency,
		Provider:    payment.Provider,
		PhoneNumber: payment.PhoneNumber,
		ExternalID:  payment.ID.String(),
		Description: description,
		Metadata:    map[string]string{"purpose": payment.Purpose},
	})
	var refused *kpay.RefusedError
	switch {
	case errors.Is(err, kpay.ErrUnknown):
		// it may have started: its webhook will tell
		logFor(c).Warn("payment outcome unknown", "payment", payment.ID, "error", err)
	case errors.As(err, &refused):
		logFor(c).Warn("payment refused by kpay", "payment", payment.ID, "status", refused.Status, "reason", refused.Message)
		s.failPayment(c, payment.ID, refusalCode(refused))
	case err != nil:
		logFor(c).Error("payment not sent to kpay", "payment", payment.ID, "error", err)
		s.failPayment(c, payment.ID, "PAYMENTS_UNAVAILABLE")
	case started.Status == kpay.Failed || started.Status == kpay.Cancelled:
		s.failPayment(c, payment.ID, "UNSPECIFIED_FAILURE")
	default:
		if err := s.store.MarkPaymentAccepted(c, db.MarkPaymentAcceptedParams{ID: payment.ID, ProviderPaymentID: started.ID}); err != nil {
			internalServerError(c, err)
			return
		}
	}
	if payment, err = s.store.GetPayment(c, payment.ID); err != nil {
		internalServerError(c, err)
		return
	}
	logFor(c).Info("payment started", "payment", payment.ID, "purpose", payment.Purpose, "status", payment.Status)
	apiResponse(c, http.StatusCreated, toPaymentResponse(payment))
}

// startCardPayment keeps the payment, then asks KPAY for its hosted page,
// whose address is answered (gateway_url) for the payer to open.
func (s *Server) startCardPayment(c *gin.Context, params db.CreatePaymentParams, payer payerRequest, description string) {
	params.ID = uuid.New()
	params.Provider = cardProvider
	payment, err := s.store.CreatePayment(c, params)
	if err != nil {
		internalServerError(c, err)
		return
	}
	started, err := s.money.Init(c, kpay.InitRequest{
		Amount:        payment.Amount,
		Currency:      payment.Currency,
		PaymentMethod: kpay.CardPayment,
		ReturnURL:     s.returnURL(payer.ReturnURL, payment.ID),
		ExternalID:    payment.ID.String(),
		Description:   description,
		Metadata:      map[string]string{"purpose": payment.Purpose},
	})
	var refused *kpay.RefusedError
	switch {
	case errors.As(err, &refused):
		logFor(c).Warn("card payment refused by kpay", "payment", payment.ID, "status", refused.Status, "reason", refused.Message)
		s.failPayment(c, payment.ID, refusalCode(refused))
	case err != nil:
		logFor(c).Error("card payment not sent to kpay", "payment", payment.ID, "error", err)
		s.failPayment(c, payment.ID, "PAYMENTS_UNAVAILABLE")
	case started.GatewayURL == "":
		logFor(c).Error("kpay gave no page for a card payment", "payment", payment.ID)
		s.failPayment(c, payment.ID, "PAYMENTS_UNAVAILABLE")
	default:
		if err := s.store.MarkPaymentAccepted(c, db.MarkPaymentAcceptedParams{ID: payment.ID, ProviderPaymentID: started.ID}); err != nil {
			internalServerError(c, err)
			return
		}
	}
	if payment, err = s.store.GetPayment(c, payment.ID); err != nil {
		internalServerError(c, err)
		return
	}
	response := toPaymentResponse(payment)
	if payment.Status == "accepted" {
		response.GatewayURL = started.GatewayURL
	}
	logFor(c).Info("card payment started", "payment", payment.ID, "purpose", payment.Purpose, "status", payment.Status)
	apiResponse(c, http.StatusCreated, response)
}

// refusalCode: why KPAY refused a payment, as a code the apps translate.
func refusalCode(refused *kpay.RefusedError) string {
	message := strings.ToLower(refused.Message)
	switch {
	case refused.Status == http.StatusUnauthorized || refused.Status == http.StatusForbidden:
		return "PAYMENTS_UNAVAILABLE"
	case refused.Status == http.StatusTooManyRequests:
		return "PROVIDER_TEMPORARILY_UNAVAILABLE"
	// a sandbox key (kpay_test_…) only takes KPAY's test numbers
	case strings.Contains(message, "test"):
		return "SANDBOX_TEST_NUMBER"
	case strings.Contains(message, "minimum") || strings.Contains(message, "maximum") || strings.Contains(message, "amount"):
		return "AMOUNT_OUT_OF_BOUNDS"
	case strings.Contains(message, "phone"):
		return "PAYER_NOT_FOUND"
	default:
		return "REJECTED"
	}
}

// checkOperator: [provider] is offered, and takes [amount] in [currency].
func checkOperator(operators []kpay.Operator, provider, currency string, amount int64) (string, error) {
	for _, operator := range operators {
		if operator.Provider != provider {
			continue
		}
		if operator.Currency != currency {
			return "PROVIDER_UNAVAILABLE", fmt.Errorf("%s does not take %s", provider, currency)
		}
		if low, err := strconv.ParseInt(operator.MinAmount, 10, 64); err == nil && amount < low {
			return "AMOUNT_OUT_OF_BOUNDS", fmt.Errorf("%s takes at least %d %s", provider, low, currency)
		}
		return "", nil
	}
	return "PROVIDER_UNAVAILABLE", fmt.Errorf("%s is not offered", provider)
}

func (s *Server) failPayment(ctx context.Context, id uuid.UUID, code string) {
	if _, err := s.store.FailPayment(ctx, db.FailPaymentParams{ID: id, FailureCode: code}); err != nil {
		slog.Error("cannot mark a payment failed", "payment", id, "error", err)
	}
}

type eventPaymentRequest struct {
	registrationRequest
	payerRequest
}

// POST /v1/events/:id/payments: pays the registration to a paid event (the
// price times the people); the registration is made once the money arrived.
func (s *Server) payForEvent(c *gin.Context) {
	if !s.paymentsEnabled(c) {
		return
	}
	event, ok := s.loadEvent(c, false)
	if !ok {
		return
	}
	var req eventPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if !event.PriceAmount.Valid {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "FREE_EVENT", fmt.Errorf("this event is free")))
		return
	}
	if req.People == 0 {
		req.People = 1
	}
	if req.People < 1 || req.People > 50 {
		badRequestError(c, fmt.Errorf("people: 1 to 50"))
		return
	}
	if !event.AsksLodging {
		req.NeedsLodging = false
	}
	if !event.AsksDepartureCity {
		req.DepartureCity = ""
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	registration, err := s.store.GetEventRegistration(c, db.GetEventRegistrationParams{EventID: event.ID, UserID: authUser.ID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		internalServerError(c, err)
		return
	}
	if err == nil && registration.PaymentID.Valid {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "ALREADY_PAID", fmt.Errorf("already registered and paid")))
		return
	}
	// the places now (they are counted again when the money arrives)
	others, err := s.store.SumOtherRegistrationsPeople(c, db.SumOtherRegistrationsPeopleParams{EventID: event.ID, UserID: authUser.ID})
	if err != nil {
		internalServerError(c, err)
		return
	}
	switch db.RegistrationState(event, time.Now(), others) {
	case "open":
		if event.Capacity.Valid && others+req.People > event.Capacity.Int32 {
			c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "EVENT_FULL", db.ErrEventFull))
			return
		}
	case "full":
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "EVENT_FULL", db.ErrEventFull))
		return
	default:
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "REGISTRATION_CLOSED", db.ErrRegistrationClosed))
		return
	}

	s.startPayment(c, db.CreatePaymentParams{
		UserID:        nullID(authUser.ID),
		Purpose:       paymentForEvent,
		EventID:       nullID(event.ID),
		People:        req.People,
		NeedsLodging:  req.NeedsLodging,
		DepartureCity: strings.TrimSpace(req.DepartureCity),
		Amount:        event.PriceAmount.Int64 * int64(req.People),
		Currency:      event.PriceCurrency,
	}, req.payerRequest, paymentStatementText+" · "+event.Title)
}

// ----- Premium, sold on the website -----

type premiumPlanResponse struct {
	Plan   string `json:"plan"`
	Amount int64  `json:"amount"`
}

// GET /v1/premium/plans: the plans and their prices (no account needed).
func (s *Server) listPremiumPlans(c *gin.Context) {
	plans := []premiumPlanResponse{}
	for _, plan := range premiumPlans {
		if price, ok := s.config.Pay.PremiumPrices[plan]; ok {
			plans = append(plans, premiumPlanResponse{Plan: plan, Amount: price})
		}
	}
	apiResponse(c, http.StatusOK, gin.H{
		"enabled":  s.money != nil && s.premium != nil && len(plans) > 0,
		"currency": s.config.Pay.PremiumCurrency,
		"plans":    plans,
	})
}

type premiumPaymentRequest struct {
	Plan string `json:"plan" binding:"required"`
	payerRequest
}

// POST /v1/premium/payments: pays a Premium plan; Premium is given once the
// money arrived.
func (s *Server) payForPremium(c *gin.Context) {
	if !s.paymentsEnabled(c) {
		return
	}
	if s.premium == nil {
		c.JSON(http.StatusServiceUnavailable, NewErrorResponse(http.StatusServiceUnavailable,
			"PAYMENTS_DISABLED", fmt.Errorf("premium is not sold here")))
		return
	}
	var req premiumPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	price, ok := s.config.Pay.PremiumPrices[req.Plan]
	if !ok || !slices.Contains(premiumPlans, req.Plan) {
		badRequestError(c, fmt.Errorf("unknown plan %q", req.Plan))
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	s.startPayment(c, db.CreatePaymentParams{
		UserID:      nullID(authUser.ID),
		Purpose:     paymentForPremium,
		People:      1,
		PremiumPlan: req.Plan,
		Amount:      price,
		Currency:    s.config.Pay.PremiumCurrency,
	}, req.payerRequest, paymentStatementText+" Premium")
}

// GET /v1/premium/status: the Premium bought here (not in the stores):
// for life, or until when; null until when there is none.
func (s *Server) getPremiumStatus(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	lifetime, until, err := s.premiumBought(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	if until != nil && until.Before(time.Now()) {
		until = nil
	}
	apiResponse(c, http.StatusOK, gin.H{"lifetime": lifetime, "until": until})
}

// premiumBought: the Premium [userID] has from here (bought on the website,
// offered by the admins), for life or until when.
func (s *Server) premiumBought(ctx context.Context, userID int64) (bool, *time.Time, error) {
	payments, err := s.store.ListUserPremiumPayments(ctx, nullID(userID))
	if err != nil {
		return false, nil, err
	}
	var until *time.Time
	later := func(end time.Time) {
		if until == nil || end.After(*until) {
			until = &end
		}
	}
	for _, payment := range payments {
		if !payment.PremiumUntil.Valid {
			return true, nil, nil
		}
		later(payment.PremiumUntil.Time)
	}
	gifts, err := s.store.ListUserGivenGifts(ctx, userID)
	if err != nil {
		return false, nil, err
	}
	for _, gift := range gifts {
		if gift.Until.Valid {
			later(gift.Until.Time)
		}
	}
	return false, until, nil
}

// ----- following a payment -----

// GET /v1/payments/:id: the member waits for the outcome (KPAY's webhook
// updates it).
func (s *Server) getPayment(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		resourceNotFoundError(c, "Payment")
		return
	}
	payment, err := s.store.GetPayment(c, id)
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "Payment")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if payment.UserID.Int64 != authUser.ID && !s.isStaff(c) {
		resourceNotFoundError(c, "Payment")
		return
	}
	apiResponse(c, http.StatusOK, toPaymentResponse(payment))
}

// POST /v1/kpay/callbacks[/deposits|/refunds|…]: KPAY tells a final status
// (relayed by cmfi-connect.com/api/kpay/*). Believed only when X-KPAY-Signature
// is the HMAC of the body with the webhook secret: else 401, which KPAY
// doesn't send again. Sent several times, it does its work once.
func (s *Server) kpayWebhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		badRequestError(c, err)
		return
	}
	if !kpay.VerifySignature(body, c.GetHeader("X-KPAY-Signature"), s.config.Pay.KPayWebhookSecret) {
		logFor(c).Warn("kpay webhook refused: bad signature")
		unauthorizedError(c)
		return
	}
	var hook kpay.Webhook
	if err := json.Unmarshal(body, &hook); err != nil {
		badRequestError(c, err)
		return
	}
	if hook.Event == "" {
		hook.Event = c.GetHeader("X-KPAY-Event")
	}
	switch {
	case strings.HasPrefix(hook.Event, "payment."):
		s.onPaymentWebhook(c, hook)
	case strings.HasPrefix(hook.Event, "refund."):
		s.onRefundWebhook(c, hook)
	case strings.HasPrefix(hook.Event, "payout."):
		s.onPayoutWebhook(c, hook)
	default:
		logFor(c).Info("kpay webhook ignored", "event", hook.Event)
	}
	c.JSON(http.StatusOK, gin.H{"received": true})
}

// webhookFailure: why KPAY says it failed, as a code ("CANCELLED" when the
// member cancelled it).
func webhookFailure(hook kpay.Webhook) string {
	switch {
	case hook.FailureCode != "":
		return hook.FailureCode
	case hook.Status == kpay.Cancelled || strings.HasSuffix(hook.Event, ".cancelled"):
		return "CANCELLED"
	case hook.FailureReason != "" && strings.ToUpper(hook.FailureReason) == hook.FailureReason && !strings.Contains(hook.FailureReason, " "):
		return hook.FailureReason
	default:
		return "UNSPECIFIED_FAILURE"
	}
}

func (s *Server) onPaymentWebhook(ctx context.Context, hook kpay.Webhook) {
	payment, ok := s.paymentOf(ctx, hook)
	if !ok {
		slog.Warn("kpay webhook for an unknown payment", "event", hook.Event)
		return
	}
	if hook.PaymentID != "" && payment.ProviderPaymentID == "" {
		_ = s.store.SetPaymentProviderID(ctx, db.SetPaymentProviderIDParams{ID: payment.ID, ProviderPaymentID: hook.PaymentID})
	}
	switch {
	case hook.Event == "payment.completed" || hook.Status == kpay.Completed:
		// a card is charged in USD: its amount is not the one asked
		if amount, err := strconv.ParseFloat(fmt.Sprint(hook.Amount), 64); err == nil &&
			payment.Provider != cardProvider && int64(amount) < payment.Amount {
			slog.Error("kpay says completed for less than asked: left pending", "payment", payment.ID)
			return
		}
		completed, err := s.store.CompletePayment(ctx, db.CompletePaymentParams{ID: payment.ID, ProviderTransactionID: hook.Reference})
		if errors.Is(err, sql.ErrNoRows) {
			return // known already
		}
		if err != nil {
			slog.Error("cannot mark a payment completed", "payment", payment.ID, "error", err)
			return
		}
		slog.Info("payment completed", "payment", payment.ID, "purpose", payment.Purpose)
		s.settle(ctx, completed)
	case hook.Event == "payment.failed" || hook.Event == "payment.cancelled" ||
		hook.Status == kpay.Failed || hook.Status == kpay.Cancelled:
		s.failPayment(ctx, payment.ID, webhookFailure(hook))
		slog.Info("payment failed", "payment", payment.ID, "code", webhookFailure(hook))
	}
}

// paymentOf: the payment a webhook is about (our id is its externalId; else
// KPAY's id).
func (s *Server) paymentOf(ctx context.Context, hook kpay.Webhook) (db.Payment, bool) {
	if id, err := uuid.Parse(hook.ExternalID); err == nil {
		if payment, err := s.store.GetPayment(ctx, id); err == nil {
			return payment, true
		}
	}
	if hook.PaymentID != "" {
		if payment, err := s.store.GetPaymentByProviderID(ctx, hook.PaymentID); err == nil {
			return payment, true
		}
	}
	return db.Payment{}, false
}

func (s *Server) onRefundWebhook(ctx context.Context, hook kpay.Webhook) {
	var payment db.Payment
	found := false
	if id, err := uuid.Parse(hook.ExternalID); err == nil {
		payment, err = s.store.GetPaymentByRefundID(ctx, uuid.NullUUID{UUID: id, Valid: true})
		found = err == nil
	}
	if !found && hook.PaymentID != "" {
		var err error
		payment, err = s.store.GetPaymentByProviderRefundID(ctx, hook.PaymentID)
		found = err == nil
	}
	if !found {
		slog.Warn("kpay webhook for an unknown refund", "event", hook.Event)
		return
	}
	switch {
	case hook.Event == "refund.completed" || hook.Status == kpay.Completed:
		refunded, err := s.store.CompletePaymentRefund(ctx, payment.RefundID)
		if errors.Is(err, sql.ErrNoRows) {
			return
		}
		if err != nil {
			slog.Error("cannot mark a refund completed", "payment", payment.ID, "error", err)
			return
		}
		slog.Info("payment refunded", "payment", payment.ID)
		s.afterRefund(ctx, refunded)
	case hook.Event == "refund.failed" || hook.Event == "refund.cancelled" ||
		hook.Status == kpay.Failed || hook.Status == kpay.Cancelled:
		s.failRefund(ctx, payment.RefundID, webhookFailure(hook))
	}
}

// ReconcilePayments finishes, every minute, what stays undone until [ctx]
// ends: the work of a completed payment, a refund KPAY never got, a payment
// KPAY never acknowledged.
func (s *Server) ReconcilePayments(ctx context.Context) {
	if s.money == nil && s.premium == nil {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.money != nil {
				s.reconcileOpenPayments(ctx, time.Now().Add(-time.Minute))
			}
			// the premium gifts RevenueCat refused, again
			if s.premium != nil {
				s.giveGifts(ctx, sql.NullInt64{}, 50)
			}
		}
	}
}

func (s *Server) reconcileOpenPayments(ctx context.Context, before time.Time) {
	payments, err := s.store.ListOpenPayments(ctx, before)
	if err != nil {
		slog.Error("cannot list the open payments", "error", err)
		return
	}
	for _, payment := range payments {
		s.reconcile(ctx, payment)
	}
}

// reconcile does what is left to do for [payment]. KPAY can't be asked
// about a payment: one it accepted waits for its webhook.
func (s *Server) reconcile(ctx context.Context, payment db.Payment) {
	switch {
	case payment.Status == "completed" && !payment.SettledAt.Valid:
		s.settle(ctx, payment)
	case payment.Status == "refund_pending" && payment.ProviderRefundID == "" && time.Since(payment.UpdatedAt) > 2*time.Minute:
		// the refund never got to KPAY: sent again (the same externalId)
		s.sendRefund(ctx, payment)
		_ = s.store.TouchPaymentCheck(ctx, payment.ID)
	case payment.Status == "initiated" && time.Since(payment.CreatedAt) > 30*time.Minute:
		// KPAY never acknowledged it, nor sent a webhook
		s.failPayment(ctx, payment.ID, "NOT_FOUND")
	default:
		_ = s.store.TouchPaymentCheck(ctx, payment.ID)
	}
}

// settle does the work of a completed payment: the registration, or Premium.
// When it can't now, the reconciler tries again.
func (s *Server) settle(ctx context.Context, payment db.Payment) {
	switch payment.Purpose {
	case paymentForEvent:
		s.settleEventPayment(ctx, payment)
	case paymentForPremium:
		s.settlePremiumPayment(ctx, payment)
	}
}

func (s *Server) settleEventPayment(ctx context.Context, payment db.Payment) {
	if !payment.EventID.Valid || !payment.UserID.Valid {
		// the event or the member is gone: the money goes back
		s.refundPayment(ctx, payment, refundEventUnavailable)
		return
	}
	// the deadline as when they paid; the places as now
	_, err := s.store.RegisterForEventTx(ctx, db.UpsertEventRegistrationParams{
		EventID:       payment.EventID.Int64,
		UserID:        payment.UserID.Int64,
		People:        payment.People,
		NeedsLodging:  payment.NeedsLodging,
		DepartureCity: payment.DepartureCity,
	}, payment.CreatedAt)
	switch {
	case errors.Is(err, db.ErrEventFull), errors.Is(err, db.ErrRegistrationClosed), errors.Is(err, db.ErrEventNotFound):
		slog.Info("paid event no longer available: refunded", "payment", payment.ID, "reason", err)
		s.refundPayment(ctx, payment, refundEventUnavailable)
		return
	case err != nil:
		slog.Error("cannot register a paid member", "payment", payment.ID, "error", err)
		return
	}
	if err := s.store.SetRegistrationPayment(ctx, db.SetRegistrationPaymentParams{
		PaymentID: uuid.NullUUID{UUID: payment.ID, Valid: true},
		EventID:   payment.EventID.Int64,
		UserID:    payment.UserID.Int64,
	}); err != nil {
		slog.Error("cannot link a registration to its payment", "payment", payment.ID, "error", err)
		return
	}
	if err := s.store.SettlePayment(ctx, db.SettlePaymentParams{ID: payment.ID}); err != nil {
		slog.Error("cannot settle a payment", "payment", payment.ID, "error", err)
	}
}

func (s *Server) settlePremiumPayment(ctx context.Context, payment db.Payment) {
	if !payment.UserID.Valid {
		// the account was deleted meanwhile
		_ = s.store.SettlePayment(ctx, db.SettlePaymentParams{ID: payment.ID})
		return
	}
	if s.premium == nil {
		slog.Error("premium paid but REVENUECAT_SECRET_KEY is not set: waiting", "payment", payment.ID)
		return
	}
	// a plan bought before its end follows it
	lifetime, current, err := s.premiumBought(ctx, payment.UserID.Int64)
	if err != nil {
		slog.Error("cannot read the premium of a member", "payment", payment.ID, "error", err)
		return
	}
	from := time.Now()
	if current != nil && current.After(from) {
		from = *current
	}
	until := premiumEnd(payment.PremiumPlan, from)
	if lifetime {
		until = nil
	}
	if err := s.premium.Grant(ctx, strconv.FormatInt(payment.UserID.Int64, 10), until); err != nil {
		slog.Error("cannot give premium: will try again", "payment", payment.ID, "error", err)
		return
	}
	settled := db.SettlePaymentParams{ID: payment.ID}
	if until != nil {
		settled.PremiumUntil = sql.NullTime{Time: *until, Valid: true}
	}
	if err := s.store.SettlePayment(ctx, settled); err != nil {
		slog.Error("cannot settle a payment", "payment", payment.ID, "error", err)
		return
	}
	slog.Info("premium given", "payment", payment.ID, "plan", payment.PremiumPlan)
}

// ----- refunds -----

var errNotRefundable = errors.New("only a completed payment can be refunded")

// refundPayment sends the money of [payment] back to the member's phone.
func (s *Server) refundPayment(ctx context.Context, payment db.Payment, reason string) (db.Payment, error) {
	refunding, err := s.store.StartPaymentRefund(ctx, db.StartPaymentRefundParams{
		ID:           payment.ID,
		RefundID:     uuid.NullUUID{UUID: uuid.New(), Valid: true},
		RefundReason: reason,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return payment, errNotRefundable
	}
	if err != nil {
		return payment, err
	}
	s.sendRefund(ctx, refunding)
	return s.store.GetPayment(ctx, payment.ID)
}

// sendRefund asks KPAY to give the whole payment back to its payer (again,
// with the same externalId, when it never got there).
func (s *Server) sendRefund(ctx context.Context, payment db.Payment) {
	if payment.ProviderPaymentID == "" {
		slog.Error("refund impossible: kpay id unknown", "payment", payment.ID)
		s.failRefund(ctx, payment.RefundID, "PAYMENT_NOT_FOUND")
		return
	}
	refund, err := s.money.Refund(ctx, payment.ProviderPaymentID, kpay.RefundRequest{
		Reason:     payment.RefundReason,
		ExternalID: payment.RefundID.UUID.String(),
	})
	var refused *kpay.RefusedError
	switch {
	case errors.Is(err, kpay.ErrUnknown):
		slog.Warn("refund outcome unknown", "payment", payment.ID, "error", err)
	case errors.Is(err, kpay.ErrDuplicate):
		slog.Info("refund already sent", "payment", payment.ID)
	case errors.As(err, &refused):
		slog.Warn("refund refused by kpay", "payment", payment.ID, "status", refused.Status, "reason", refused.Message)
		s.failRefund(ctx, payment.RefundID, refundRefusalCode(refused))
	case err != nil:
		slog.Error("refund not sent to kpay", "payment", payment.ID, "error", err)
		s.failRefund(ctx, payment.RefundID, "REFUNDS_UNAVAILABLE")
	case refund.Status == kpay.Failed:
		s.failRefund(ctx, payment.RefundID, "UNSPECIFIED_FAILURE")
	default:
		if err := s.store.SetPaymentProviderRefundID(ctx, db.SetPaymentProviderRefundIDParams{ID: payment.ID, ProviderRefundID: refund.ID}); err != nil {
			slog.Error("cannot keep the kpay refund id", "payment", payment.ID, "error", err)
		}
		slog.Info("refund started", "payment", payment.ID, "reason", payment.RefundReason)
	}
}

// refundRefusalCode: why KPAY refused a refund (7 days past, wallet short…).
func refundRefusalCode(refused *kpay.RefusedError) string {
	message := strings.ToLower(refused.Message)
	switch {
	case strings.Contains(message, "7 jours") || strings.Contains(message, "fenetre") || strings.Contains(message, "fenêtre"):
		return "REFUND_WINDOW_PASSED"
	case strings.Contains(message, "solde") || strings.Contains(message, "balance"):
		return "WALLET_BALANCE_TOO_LOW"
	case strings.Contains(message, "deja") || strings.Contains(message, "déjà") || strings.Contains(message, "already"):
		return "ALREADY_REFUNDED"
	default:
		return "REFUND_REFUSED"
	}
}

func (s *Server) failRefund(ctx context.Context, refundID uuid.NullUUID, code string) {
	if _, err := s.store.FailPaymentRefund(ctx, db.FailPaymentRefundParams{RefundID: refundID, FailureCode: code}); err != nil {
		slog.Error("cannot mark a refund failed", "error", err)
	}
}

// afterRefund takes back what the payment gave: the place, or Premium.
func (s *Server) afterRefund(ctx context.Context, payment db.Payment) {
	switch payment.Purpose {
	case paymentForEvent:
		if _, err := s.store.DeleteRegistrationOfPayment(ctx, uuid.NullUUID{UUID: payment.ID, Valid: true}); err != nil {
			slog.Error("cannot remove a refunded registration", "payment", payment.ID, "error", err)
		}
	case paymentForPremium:
		if s.premium == nil || !payment.UserID.Valid || !payment.SettledAt.Valid {
			return
		}
		if err := s.premium.Revoke(ctx, strconv.FormatInt(payment.UserID.Int64, 10)); err != nil {
			slog.Error("cannot take back premium", "payment", payment.ID, "error", err)
			return
		}
		// what other payments still give, given again
		lifetime, until, err := s.premiumBought(ctx, payment.UserID.Int64)
		if err == nil && (lifetime || until != nil && until.After(time.Now())) {
			if lifetime {
				until = nil
			}
			if err := s.premium.Grant(ctx, strconv.FormatInt(payment.UserID.Int64, 10), until); err != nil {
				slog.Error("cannot give back the other premium", "payment", payment.ID, "error", err)
			}
		}
	}
}

// ----- admins -----

type adminPaymentResponse struct {
	paymentResponse
	// initiated, accepted, completed, failed, refund_pending, refunded,
	// refund_failed
	RawStatus             string      `json:"raw_status"`
	Member                *memberCard `json:"member"`
	PhoneNumber           string      `json:"phone_number"`
	ProviderTransactionID string      `json:"provider_transaction_id"`
	RefundReason          string      `json:"refund_reason"`
	RefundedAt            *time.Time  `json:"refunded_at"`
	// its money was sent to the event's account
	Forwarded bool `json:"forwarded"`
}

type listPaymentsRequest struct {
	EventID int64  `form:"event_id"`
	Purpose string `form:"purpose"`
	Page    int32  `form:"page"`
	Size    int32  `form:"size"`
}

// GET /v1/admin/payments?event_id=&purpose=&page=&size=: latest first.
func (s *Server) adminListPayments(c *gin.Context) {
	var req listPaymentsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	limit, offset := listEventsRequest{Page: req.Page, Size: req.Size}.limits()
	payments, err := s.store.ListPayments(c, db.ListPaymentsParams{
		EventID: nullID(req.EventID), Purpose: req.Purpose, Lim: limit, Off: offset,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	ids := []int64{}
	for _, payment := range payments {
		if payment.UserID.Valid {
			ids = append(ids, payment.UserID.Int64)
		}
	}
	cards := map[int64]memberCard{}
	if len(ids) > 0 {
		found, err := s.store.ListMemberCards(c, ids)
		if err != nil {
			internalServerError(c, err)
			return
		}
		for _, card := range found {
			cards[card.ID] = toMemberCard(card)
		}
	}
	response := make([]adminPaymentResponse, 0, len(payments))
	for _, payment := range payments {
		item := adminPaymentResponse{
			paymentResponse:       toPaymentResponse(payment),
			RawStatus:             payment.Status,
			PhoneNumber:           payment.PhoneNumber,
			ProviderTransactionID: payment.ProviderTransactionID,
			RefundReason:          payment.RefundReason,
			RefundedAt:            timePtr(payment.RefundedAt),
			Forwarded:             payment.PayoutID.Valid,
		}
		if card, ok := cards[payment.UserID.Int64]; ok {
			item.Member = &card
		}
		response = append(response, item)
	}
	apiResponse(c, http.StatusOK, response)
}

// POST /v1/admin/payments/:id/refund: the money back to the member's phone;
// once refunded, their registration (or Premium) goes.
func (s *Server) adminRefundPayment(c *gin.Context) {
	if !s.paymentsEnabled(c) {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		resourceNotFoundError(c, "Payment")
		return
	}
	payment, err := s.store.GetPayment(c, id)
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "Payment")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	refunding, err := s.refundPayment(c, payment, refundByAdmin)
	if errors.Is(err, errNotRefundable) {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "NOT_REFUNDABLE", err))
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	logFor(c).Info("refund asked by an admin", "payment", payment.ID)
	apiResponse(c, http.StatusOK, toPaymentResponse(refunding))
}
