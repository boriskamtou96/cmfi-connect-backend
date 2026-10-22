package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/kpay"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const testWebhookSecret = "whsec-test"

type askedRefund struct {
	paymentID string
	req       kpay.RefundRequest
}

// fakeMoney is KPAY: payments and refunds are taken, the test then says how
// they end with a signed webhook ([paymentsWorld.notify]).
type fakeMoney struct {
	mu sync.Mutex
	// by externalId (our payment id)
	inits map[string]kpay.InitRequest
	// by externalId (our refund id), with KPAY's payment id
	refunds map[string]askedRefund
	// what a new payment answers instead of PENDING
	initErr error
	// the sendings from the wallet, by externalId; its balance
	withdrawals map[string]kpay.WithdrawRequest
	available   float64
}

func newFakeMoney() *fakeMoney {
	return &fakeMoney{
		inits: map[string]kpay.InitRequest{}, refunds: map[string]askedRefund{},
		withdrawals: map[string]kpay.WithdrawRequest{}, available: 1_000_000,
	}
}

func (f *fakeMoney) Init(_ context.Context, req kpay.InitRequest) (kpay.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inits[req.ExternalID] = req
	if f.initErr != nil {
		return kpay.Payment{}, f.initErr
	}
	payment := kpay.Payment{ID: kpayID(req.ExternalID), Reference: "KPAY-" + req.ExternalID[:8], Status: kpay.Pending}
	if req.PaymentMethod == kpay.CardPayment {
		payment.GatewayURL = "https://admin.kpay.site/pay/gw_" + req.ExternalID[:8]
	}
	return payment, nil
}

// kpayID: the id KPAY gives a payment of ours.
func kpayID(externalID string) string { return "kp_" + externalID[:8] }

func (f *fakeMoney) Refund(_ context.Context, paymentID string, req kpay.RefundRequest) (kpay.Refund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refunds[req.ExternalID] = askedRefund{paymentID, req}
	return kpay.Refund{ID: "rf_" + req.ExternalID[:8], Status: kpay.Pending}, nil
}

// refundOf: our refund id of the payment [externalID], if one was asked.
func (f *fakeMoney) refundOf(externalID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, refund := range f.refunds {
		if refund.paymentID == kpayID(externalID) {
			return id, true
		}
	}
	return "", false
}

func (f *fakeMoney) PredictProvider(_ context.Context, phone string) (kpay.Prediction, error) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if !strings.HasPrefix(digits, "237") || len(digits) != 12 {
		return kpay.Prediction{}, kpay.ErrInvalidPhone
	}
	provider := "MTN_MOMO_CMR"
	if strings.HasPrefix(digits, "23769") {
		provider = "ORANGE_CMR"
	}
	return kpay.Prediction{Country: "CMR", Provider: provider, PhoneNumber: digits}, nil
}

func (f *fakeMoney) Withdraw(_ context.Context, req kpay.WithdrawRequest) (kpay.Withdrawal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.withdrawals[req.ExternalID] = req
	return kpay.Withdrawal{ID: "wd_" + req.ExternalID[:8], Status: kpay.Pending}, nil
}

func (f *fakeMoney) Balances(context.Context) ([]kpay.Balance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []kpay.Balance{{Currency: "XAF", AvailableBalance: f.available}}, nil
}

func (f *fakeMoney) PlatformFees(context.Context) (kpay.Fees, error) {
	return kpay.Fees{PaymentRate: 0.05, WithdrawalRate: 0.05}, nil
}

// withdrawalTo: our ids of the sendings asked to [phone], oldest unknown.
func (f *fakeMoney) withdrawalsTo(phone string) map[string]kpay.WithdrawRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	found := map[string]kpay.WithdrawRequest{}
	for id, req := range f.withdrawals {
		if req.PhoneNumber == phone {
			found[id] = req
		}
	}
	return found
}

// fakePremium is RevenueCat: what each member was given (nil: for life).
type fakePremium struct {
	mu      sync.Mutex
	granted map[string][]*time.Time
	revoked map[string]int
}

func (f *fakePremium) Grant(_ context.Context, user string, until *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.granted[user] = append(f.granted[user], until)
	return nil
}

func (f *fakePremium) Revoke(_ context.Context, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[user]++
	return nil
}

type paymentsWorld struct {
	router  http.Handler
	server  *Server
	money   *fakeMoney
	premium *fakePremium
}

func newPaymentsWorld(t *testing.T) paymentsWorld {
	server, err := New(testStore, &utils.Config{
		Token: utils.TokenConfig{SecretKey: strings.Repeat("k", 32), Duration: "15m"},
		Pay: utils.PaymentsConfig{
			KPayWebhookSecret: testWebhookSecret,
			KPayCards:         true,
			ReturnURL:         "https://cmfi-connect.com/paiement",
			PremiumPrices:     map[string]int64{"monthly": 1500, "yearly": 12000, "lifetime": 50000},
			PremiumCurrency:   "XAF",
		},
	})
	require.NoError(t, err)
	world := paymentsWorld{
		server:  server,
		money:   newFakeMoney(),
		premium: &fakePremium{granted: map[string][]*time.Time{}, revoked: map[string]int{}},
	}
	server.money = world.money
	server.premium = world.premium
	world.router = server.SetupRouter()
	// the test payments go with the test
	t.Cleanup(func() {
		world.money.mu.Lock()
		ids := []string{}
		for id := range world.money.inits {
			ids = append(ids, id)
		}
		world.money.mu.Unlock()
		for _, id := range ids {
			runSQL(t, `DELETE FROM payments WHERE id = $1`, id)
		}
		for id := range world.money.withdrawals {
			runSQL(t, `DELETE FROM payouts WHERE id = $1`, id)
		}
	})
	return world
}

// runSQL runs a statement on the test database.
func runSQL(t *testing.T, query string, args ...any) {
	conn, err := sql.Open(dbDriver, dsn)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(context.Background(), query, args...)
	require.NoError(t, err)
}

// rawContent: the content of an answer, as sent.
type rawContent struct {
	Content json.RawMessage `json:"content"`
}

func (w paymentsWorld) pay(t *testing.T, token, path string, body map[string]any) paymentResponse {
	t.Helper()
	response := send(t, w.router, http.MethodPost, token, path, body)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	return decode[paymentResponse](t, response)
}

func (w paymentsWorld) payment(t *testing.T, token, id string) paymentResponse {
	t.Helper()
	response := send(t, w.router, http.MethodGet, token, "/v1/payments/"+id, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	return decode[paymentResponse](t, response)
}

// webhook posts [body] to the callback, signed with [secret].
func (w paymentsWorld) webhook(t *testing.T, body map[string]any, secret string) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/v1/kpay/callbacks/deposits", bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-KPAY-Signature", kpay.Sign(data, secret))
	request.Header.Set("X-KPAY-Event", fmt.Sprint(body["event"]))
	recorder := httptest.NewRecorder()
	w.router.ServeHTTP(recorder, request)
	return recorder
}

// notify: KPAY says [event] happened to our payment (or refund) [externalID].
func (w paymentsWorld) notify(t *testing.T, event, externalID string, fields map[string]any) {
	t.Helper()
	status := strings.ToUpper(strings.SplitN(event, ".", 2)[1])
	body := map[string]any{"event": event, "externalId": externalID, "status": status, "reference": "KPAY-REF"}
	for key, value := range fields {
		body[key] = value
	}
	response := w.webhook(t, body, testWebhookSecret)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

func TestAPaidEventRegistersTheMemberOnceTheMoneyArrived(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 5000, "asks_lodging": true})
	require.Equal(t, &priceResponse{Amount: 5000, Currency: "XAF"}, event.Price)

	// registered without paying first (to pay later): a place, not paid
	later := send(t, w.router, http.MethodPut, m.Token, eventPath(event, "/registration"), map[string]any{"people": 1})
	require.Equal(t, http.StatusOK, later.Code, later.Body.String())
	require.False(t, decode[eventResponse](t, later).MyRegistration.Paid)

	// 2 people: 10 000 XAF asked to the phone, the operator found from the number
	payment := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{
		"people": 2, "needs_lodging": true, "phone_number": "+237 6 70 00 00 01",
		// a price sent by the app is ignored
		"amount": 1,
	})
	require.Equal(t, "pending", payment.Status)
	require.Equal(t, int64(10000), payment.Amount)
	require.Equal(t, "MTN_MOMO_CMR", payment.Provider)
	asked := w.money.inits[payment.ID]
	require.Equal(t, int64(10000), asked.Amount)
	require.Equal(t, "XAF", asked.Currency)
	require.Equal(t, "237670000001", asked.PhoneNumber)

	// still to pay while the member confirms
	detail := decode[eventResponse](t, send(t, w.router, http.MethodGet, m.Token, eventPath(event, ""), nil))
	require.False(t, detail.MyRegistration.Paid)

	// the money arrived: told by KPAY's signed webhook
	w.notify(t, "payment.completed", payment.ID, map[string]any{"amount": 10000, "paymentId": kpayID(payment.ID)})
	require.Equal(t, "completed", w.payment(t, m.Token, payment.ID).Status)
	detail = decode[eventResponse](t, send(t, w.router, http.MethodGet, m.Token, eventPath(event, ""), nil))
	require.True(t, detail.MyRegistration.Paid)
	require.Equal(t, int32(2), detail.MyRegistration.People)
	require.True(t, detail.MyRegistration.NeedsLodging)

	// once is enough: the same webhook again does nothing more
	w.notify(t, "payment.completed", payment.ID, map[string]any{"amount": 10000})
	again := send(t, w.router, http.MethodPost, m.Token, eventPath(event, "/payments"),
		map[string]any{"people": 1, "phone_number": "+237670000001"})
	require.Equal(t, http.StatusConflict, again.Code)
	require.Contains(t, again.Body.String(), "ALREADY_PAID")

	// the lodging may change, not the people; leaving goes through a refund
	changed := send(t, w.router, http.MethodPut, m.Token, eventPath(event, "/registration"),
		map[string]any{"people": 2, "needs_lodging": false})
	require.Equal(t, http.StatusOK, changed.Code, changed.Body.String())
	more := send(t, w.router, http.MethodPut, m.Token, eventPath(event, "/registration"), map[string]any{"people": 3})
	require.Equal(t, http.StatusConflict, more.Code)
	require.Contains(t, more.Body.String(), "PEOPLE_PAID")

	// the organizers see who paid
	registrants := decode[registrationsResponse](t, send(t, w.router, http.MethodGet, admin.Token, adminEventPath(event, "/registrations"), nil))
	require.Equal(t, int32(2), registrants.PaidPeople)
	require.True(t, registrants.Registrants[0].Paid)
	leave := send(t, w.router, http.MethodDelete, m.Token, eventPath(event, "/registration"), nil)
	require.Equal(t, http.StatusConflict, leave.Code)
	require.Contains(t, leave.Body.String(), "PAID_REGISTRATION")

	// and the event can't be deleted with the money of its members
	deleted := send(t, w.router, http.MethodDelete, admin.Token, adminEventPath(event, ""), nil)
	require.Equal(t, http.StatusConflict, deleted.Code)
	require.Contains(t, deleted.Body.String(), "EVENT_HAS_PAYMENTS")
}

func TestAWebhookThatIsNotSignedChangesNothing(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 5000})
	payment := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{"phone_number": "+237670000011"})

	forged := w.webhook(t, map[string]any{"event": "payment.completed", "externalId": payment.ID, "status": "COMPLETED"}, "guessed")
	require.Equal(t, http.StatusUnauthorized, forged.Code)
	require.Equal(t, "pending", w.payment(t, m.Token, payment.ID).Status)

	// signed, but for less than asked: not believed either
	w.notify(t, "payment.completed", payment.ID, map[string]any{"amount": 50})
	require.Equal(t, "pending", w.payment(t, m.Token, payment.ID).Status)
}

func TestAnUnpaidRegistrationCanBeCancelledOrPaidLater(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 2500})

	registered := send(t, w.router, http.MethodPut, m.Token, eventPath(event, "/registration"), map[string]any{"people": 2})
	require.Equal(t, http.StatusOK, registered.Code, registered.Body.String())
	// the people change freely while not paid, and it can be cancelled
	changed := send(t, w.router, http.MethodPut, m.Token, eventPath(event, "/registration"), map[string]any{"people": 3})
	require.Equal(t, http.StatusOK, changed.Code, changed.Body.String())

	// paid later: the registration becomes paid
	payment := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{"people": 3, "phone_number": "+237670000010"})
	require.Equal(t, int64(7500), payment.Amount)
	w.notify(t, "payment.completed", payment.ID, map[string]any{"amount": 7500})
	detail := decode[eventResponse](t, send(t, w.router, http.MethodGet, m.Token, eventPath(event, ""), nil))
	require.True(t, detail.MyRegistration.Paid)
	require.Equal(t, int32(3), detail.MyRegistration.People)

	other := registerMember(t, w.router, nil)
	require.Equal(t, http.StatusOK, send(t, w.router, http.MethodPut, other.Token, eventPath(event, "/registration"), map[string]any{"people": 1}).Code)
	cancelled := send(t, w.router, http.MethodDelete, other.Token, eventPath(event, "/registration"), nil)
	require.Equal(t, http.StatusOK, cancelled.Code, cancelled.Body.String())
}

func TestAFailedPaymentRegistersNobody(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 2000})

	payment := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{"phone_number": "+237690000002"})
	require.Equal(t, "ORANGE_CMR", payment.Provider)
	w.notify(t, "payment.failed", payment.ID, map[string]any{"failureCode": "PAYER_LIMIT_REACHED"})

	failed := w.payment(t, m.Token, payment.ID)
	require.Equal(t, "failed", failed.Status)
	require.Equal(t, "PAYER_LIMIT_REACHED", failed.FailureCode)
	detail := decode[eventResponse](t, send(t, w.router, http.MethodGet, m.Token, eventPath(event, ""), nil))
	require.Nil(t, detail.MyRegistration)

	// cancelled by the member on their phone
	again := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{"phone_number": "+237690000002"})
	w.notify(t, "payment.cancelled", again.ID, nil)
	require.Equal(t, "CANCELLED", w.payment(t, m.Token, again.ID).FailureCode)

	// someone else doesn't see it
	other := registerMember(t, w.router, nil)
	hidden := send(t, w.router, http.MethodGet, other.Token, "/v1/payments/"+payment.ID, nil)
	require.Equal(t, http.StatusNotFound, hidden.Code)
}

func TestPaymentsAreCheckedBeforeKPayIsAsked(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	free := createEvent(t, w.router, admin.Token, nil)
	paid := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 1000, "capacity": 3})
	cheap := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 20})

	cases := []struct {
		path string
		body map[string]any
		code int
		want string
	}{
		{eventPath(free, "/payments"), map[string]any{"phone_number": "+237670000003"}, http.StatusConflict, "FREE_EVENT"},
		{eventPath(paid, "/payments"), map[string]any{"phone_number": "+33 6 12 34 56 78"}, http.StatusBadRequest, "INVALID_PHONE"},
		// below the 50 XAF KPAY takes in Cameroon
		{eventPath(cheap, "/payments"), map[string]any{"phone_number": "+237670000003"}, http.StatusConflict, "AMOUNT_OUT_OF_BOUNDS"},
		{eventPath(paid, "/payments"), map[string]any{"people": 4, "phone_number": "+237670000003"}, http.StatusConflict, "EVENT_FULL"},
		{eventPath(paid, "/payments"), map[string]any{"phone_number": "+237670000003", "provider": "AIRTEL_COD"}, http.StatusConflict, "PROVIDER_UNAVAILABLE"},
	}
	for _, tc := range cases {
		response := send(t, w.router, http.MethodPost, m.Token, tc.path, tc.body)
		require.Equal(t, tc.code, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), tc.want)
	}
	require.Empty(t, w.money.inits)

	operators := decodeRaw[rawContent](t, send(t, w.router, http.MethodGet, m.Token, "/v1/payments/providers?currency=XAF", nil))
	require.Contains(t, string(operators.Content), `"provider":"MTN_MOMO_CMR"`)
	require.Contains(t, string(operators.Content), `"provider":"ORANGE_CMR"`)
	require.NotContains(t, string(operators.Content), "AIRTEL_GAB")
}

func TestARefusedPaymentIsFailedAtOnce(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 1000})
	w.money.initErr = &kpay.RefusedError{Status: http.StatusBadRequest, Message: "amount must not be less than the configured minimum"}

	payment := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{"phone_number": "+237670000004"})
	require.Equal(t, "failed", payment.Status)
	require.Equal(t, "AMOUNT_OUT_OF_BOUNDS", payment.FailureCode)
}

func TestTheLastPlaceTakenMeanwhileRefundsThePayment(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	first := registerMember(t, w.router, nil)
	second := registerMember(t, w.router, nil)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 1000, "capacity": 1})

	// both start paying for the last place; the first one's money arrives
	one := w.pay(t, first.Token, eventPath(event, "/payments"), map[string]any{"phone_number": "+237670000005"})
	two := w.pay(t, second.Token, eventPath(event, "/payments"), map[string]any{"phone_number": "+237670000006"})
	w.notify(t, "payment.completed", one.ID, map[string]any{"amount": 1000})
	w.notify(t, "payment.completed", two.ID, map[string]any{"amount": 1000})

	// the second one is refunded, not registered
	require.Equal(t, "refunding", w.payment(t, second.Token, two.ID).Status)
	refundID, ok := w.money.refundOf(two.ID)
	require.True(t, ok)
	w.notify(t, "refund.completed", refundID, nil)
	require.Equal(t, "refunded", w.payment(t, second.Token, two.ID).Status)
	detail := decode[eventResponse](t, send(t, w.router, http.MethodGet, second.Token, eventPath(event, ""), nil))
	require.Nil(t, detail.MyRegistration)
}

func TestAnAdminRefundsAPaymentAndTheRegistrationGoes(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 3000})
	payment := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{"phone_number": "+237670000007"})
	w.notify(t, "payment.completed", payment.ID, map[string]any{"amount": 3000})

	// the admins see who paid
	listed := send(t, w.router, http.MethodGet, admin.Token, fmt.Sprintf("/v1/admin/payments?event_id=%d", event.ID), nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	payments := decode[[]adminPaymentResponse](t, listed)
	require.Len(t, payments, 1)
	require.Equal(t, "completed", payments[0].RawStatus)
	require.Equal(t, "237670000007", payments[0].PhoneNumber)
	require.Equal(t, m.Username, payments[0].Member.Username)
	// not the members
	require.Equal(t, http.StatusForbidden, send(t, w.router, http.MethodGet, m.Token, "/v1/admin/payments", nil).Code)

	refund := send(t, w.router, http.MethodPost, admin.Token, "/v1/admin/payments/"+payment.ID+"/refund", nil)
	require.Equal(t, http.StatusOK, refund.Code, refund.Body.String())
	require.Equal(t, "refunding", decode[paymentResponse](t, refund).Status)
	twice := send(t, w.router, http.MethodPost, admin.Token, "/v1/admin/payments/"+payment.ID+"/refund", nil)
	require.Equal(t, http.StatusConflict, twice.Code)

	// KPAY refunds the payment it knows by its own id
	refundID, ok := w.money.refundOf(payment.ID)
	require.True(t, ok)
	require.Equal(t, "admin", w.money.refunds[refundID].req.Reason)
	w.notify(t, "refund.completed", refundID, nil)
	require.Equal(t, "refunded", w.payment(t, m.Token, payment.ID).Status)
	detail := decode[eventResponse](t, send(t, w.router, http.MethodGet, m.Token, eventPath(event, ""), nil))
	require.Nil(t, detail.MyRegistration)
	// the event can go now
	require.Equal(t, http.StatusNoContent, send(t, w.router, http.MethodDelete, admin.Token, adminEventPath(event, ""), nil).Code)
}

func TestPremiumBoughtOnTheWebsiteIsGivenInRevenueCat(t *testing.T) {
	w := newPaymentsWorld(t)
	m := registerMember(t, w.router, nil)
	user := fmt.Sprint(m.ID)

	plans := send(t, w.router, http.MethodGet, "", "/v1/premium/plans", nil)
	require.Equal(t, http.StatusOK, plans.Code)
	require.JSONEq(t, `{"enabled":true,"currency":"XAF","plans":[
		{"plan":"monthly","amount":1500},{"plan":"yearly","amount":12000},{"plan":"lifetime","amount":50000}]}`,
		string(decodeRaw[rawContent](t, plans).Content))

	// a month, then a year bought before its end: they follow each other
	month := w.pay(t, m.Token, "/v1/premium/payments", map[string]any{"plan": "monthly", "phone_number": "+237670000008"})
	require.Equal(t, int64(1500), month.Amount)
	w.notify(t, "payment.completed", month.ID, map[string]any{"amount": 1500})
	year := w.pay(t, m.Token, "/v1/premium/payments", map[string]any{"plan": "yearly", "phone_number": "+237670000008"})
	w.notify(t, "payment.completed", year.ID, map[string]any{"amount": 12000})

	granted := w.premium.granted[user]
	require.Len(t, granted, 2)
	require.WithinDuration(t, time.Now().AddDate(0, 1, 0), *granted[0], time.Minute)
	require.WithinDuration(t, time.Now().AddDate(1, 1, 0), *granted[1], time.Minute)
	status := decodeRaw[rawContent](t, send(t, w.router, http.MethodGet, m.Token, "/v1/premium/status", nil))
	require.Contains(t, string(status.Content), `"lifetime":false`)

	// unknown plans are refused
	unknown := send(t, w.router, http.MethodPost, m.Token, "/v1/premium/payments", map[string]any{"plan": "weekly", "phone_number": "+237670000008"})
	require.Equal(t, http.StatusBadRequest, unknown.Code)
}

func TestWithoutKPayNobodyPays(t *testing.T) {
	router := newTestRouter(t)
	m := registerMember(t, router, nil)
	response := send(t, router, http.MethodGet, m.Token, "/v1/payments/providers", nil)
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Contains(t, response.Body.String(), "PAYMENTS_DISABLED")
}

func TestTheReconcilerFailsAPaymentKPayNeverAcknowledged(t *testing.T) {
	w := newPaymentsWorld(t)
	m := registerMember(t, w.router, nil)
	id := uuid.New()
	_, err := testStore.CreatePayment(context.Background(), db.CreatePaymentParams{
		ID: id, UserID: nullID(m.ID), Purpose: paymentForPremium, PremiumPlan: "monthly", People: 1,
		Amount: 1500, Currency: "XAF", PhoneNumber: "237670000009", Provider: "MTN_MOMO_CMR",
	})
	require.NoError(t, err)
	t.Cleanup(func() { runSQL(t, `DELETE FROM payments WHERE id = $1`, id) })
	runSQL(t, `UPDATE payments SET created_at = now() - interval '1 hour', updated_at = now() - interval '1 hour' WHERE id = $1`, id)

	w.server.reconcileOpenPayments(context.Background(), time.Now())
	payment, err := testStore.GetPayment(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "failed", payment.Status)
	require.Equal(t, "NOT_FOUND", payment.FailureCode)
}

func TestASandboxKeyOnlyTakesTheTestNumbers(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 1000})
	w.money.initErr = &kpay.RefusedError{Status: http.StatusBadRequest,
		Message: "Numéro de test invalide pour le sandbox (CMR). En mode test, seuls les numéros de test dédiés sont acceptés"}

	payment := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{"phone_number": "+237690000012"})
	require.Equal(t, "failed", payment.Status)
	require.Equal(t, "SANDBOX_TEST_NUMBER", payment.FailureCode)
}

func TestAnEventIsPaidByCardOnKPaysPage(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 5000})

	// no number: KPAY's page asks for the card
	payment := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{"method": "card"})
	require.Equal(t, "pending", payment.Status)
	require.Equal(t, "CARD", payment.Provider)
	require.Equal(t, "https://admin.kpay.site/pay/gw_"+payment.ID[:8], payment.GatewayURL)
	asked := w.money.inits[payment.ID]
	require.Equal(t, kpay.CardPayment, asked.PaymentMethod)
	require.Empty(t, asked.PhoneNumber)
	require.Equal(t, "https://cmfi-connect.com/paiement?payment="+payment.ID, asked.ReturnURL)

	// charged in USD: completed whatever the amount said
	w.notify(t, "payment.completed", payment.ID, map[string]any{"amount": 8.3})
	detail := decode[eventResponse](t, send(t, w.router, http.MethodGet, m.Token, eventPath(event, ""), nil))
	require.True(t, detail.MyRegistration.Paid)

	// the page to come back to: only on an allowed site
	other := registerMember(t, w.router, nil)
	evil := w.pay(t, other.Token, eventPath(event, "/payments"), map[string]any{"method": "card", "return_url": "https://evil.example/steal"})
	require.True(t, strings.HasPrefix(w.money.inits[evil.ID].ReturnURL, "https://cmfi-connect.com/paiement?"))
	third := registerMember(t, w.router, nil)
	site := w.pay(t, third.Token, eventPath(event, "/payments"), map[string]any{"method": "card", "return_url": "https://cmfi-connect.com/en/premium"})
	require.Equal(t, "https://cmfi-connect.com/en/premium?payment="+site.ID, w.money.inits[site.ID].ReturnURL)

	// mobile money still needs a number
	fourth := registerMember(t, w.router, nil)
	noPhone := send(t, w.router, http.MethodPost, fourth.Token, eventPath(event, "/payments"), map[string]any{})
	require.Equal(t, http.StatusBadRequest, noPhone.Code)
}

func TestCardsCanBeTurnedOff(t *testing.T) {
	w := newPaymentsWorld(t)
	w.server.config.Pay.KPayCards = false
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 5000})

	refused := send(t, w.router, http.MethodPost, m.Token, eventPath(event, "/payments"), map[string]any{"method": "card"})
	require.Equal(t, http.StatusConflict, refused.Code)
	require.Contains(t, refused.Body.String(), "CARDS_DISABLED")
	operators := decodeRaw[rawContent](t, send(t, w.router, http.MethodGet, m.Token, "/v1/payments/providers", nil))
	require.Contains(t, string(operators.Content), `"cards":false`)
}
