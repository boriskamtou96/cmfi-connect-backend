// Package money holds what the API knows of a mobile money provider, whoever
// it is (internal/wajub today): a payment asked of a member's phone (they
// confirm with their PIN) or of a card page, its refund, a sending to an
// organizer, the balance, the fees, and the webhooks that tell how each ended.
// The provider's client turns its own words into these.
package money

import (
	"errors"
	"fmt"
)

// The statuses of a payment, a refund or a sending, as the API reads them.
const (
	Pending    = "PENDING"
	Processing = "PROCESSING"
	Completed  = "COMPLETED"
	Failed     = "FAILED"
	Cancelled  = "CANCELLED"
)

var (
	// ErrUnknown: the provider could not tell what happened (a 5xx, the
	// network). The payment may have started; its webhook will tell.
	ErrUnknown = errors.New("money: outcome unknown")
	// ErrDuplicate: this external id was already sent (a retry): it goes on.
	ErrDuplicate = errors.New("money: already sent")
	// ErrInvalidPhone: not a number one can pay from.
	ErrInvalidPhone = errors.New("money: invalid phone number")
	// ErrBalanceTooLow: the balance can't cover a sending.
	ErrBalanceTooLow = errors.New("money: balance too low")
)

// RefusedError: the provider refused the request (4xx), with its reason.
type RefusedError struct {
	Status  int
	Message string
	// Reason: the provider's own words for why, when it gives some
	// (insufficient_funds, a failure reason of the transaction…), else "".
	Reason string
	// Detail: the provider's whole answer, phone numbers hidden, cut to 1 KB,
	// for the logs only.
	Detail string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("money: %d %s", e.Status, e.Message)
}

// CardPayment: the payment method of the provider's hosted page for Visa and
// Mastercard.
const CardPayment = "CARD"

// InitRequest asks the member's phone for the payment, or opens the
// provider's hosted page for a card ([CardPayment], with [ReturnURL]: then no
// provider nor phone number).
type InitRequest struct {
	Amount        int64
	Currency      string
	Provider      string
	PhoneNumber   string
	PaymentMethod string
	ReturnURL     string
	// a card: the payer's email, which the hosted page wants
	Email       string
	ExternalID  string
	Description string
	Metadata    map[string]string
}

// Payment: what the provider answers to a new payment.
type Payment struct {
	// the provider's id, needed for a refund
	ID        string
	Reference string
	Status    string
	// the hosted page to send the payer to (a card)
	GatewayURL string
}

type RefundRequest struct {
	Reason     string
	ExternalID string
}

type Refund struct {
	ID     string
	Status string
}

// WithdrawRequest sends money from the balance to a mobile money account.
type WithdrawRequest struct {
	Amount      int64
	Currency    string
	Provider    string
	PhoneNumber string
	// who receives it, shown by the operator
	Name        string
	ExternalID  string
	Description string
}

type Withdrawal struct {
	ID        string
	Status    string
	NetAmount float64
	FeeAmount float64
}

// Balance in one currency.
type Balance struct {
	Currency         string
	Balance          float64
	ReservedBalance  float64
	AvailableBalance float64
}

// Fees: the share the provider keeps of a payment and of a sending (0.05:
// 5 %).
type Fees struct {
	PaymentRate    float64 `json:"paymentRate"`
	WithdrawalRate float64 `json:"withdrawalRate"`
}

// Prediction: the operator of a phone number, and the number as it is kept
// (237670000000).
type Prediction struct {
	Country     string
	Provider    string
	PhoneNumber string
}

// Webhook is a change of status the provider sent (its signature checked),
// in these words: payment.completed, payment.failed, payment.cancelled,
// refund.completed, refund.failed, payout.completed, payout.failed; another
// event is only read.
type Webhook struct {
	Event string
	// the provider's id of the payment, refund or sending
	PaymentID string
	// the operator's reference of the money
	Reference string
	Status    string
	Amount    any
	// our id (the payment's, the refund's or the sending's)
	ExternalID  string
	FailureCode string
	// a code (PAYER_NOT_FOUND…) or a sentence
	FailureReason string
}

// Operator is a mobile money operator the members can pay with.
type Operator struct {
	Provider string
	Name     string
	Country  string
	Names    map[string]string
	Prefix   string
	Currency string
	// the least it takes, in the currency ("" when not said)
	MinAmount string
}

// Operators: the operators the app can offer (PROVIDERS picks among them),
// with the names shown to the members. Their codes are those kept on the
// payments since the first provider, and KPAY-NOW's (23/11/2026: the six
// countries of the CFA francs it serves; Wajub only knows Cameroon's).
var Operators = []Operator{
	{"MTN_MOMO_CMR", "MTN", "CMR", cameroon, "237", "XAF", "50"},
	{"ORANGE_CMR", "Orange", "CMR", cameroon, "237", "XAF", "50"},
	{"MTN_MOMO_COG", "MTN", "COG", map[string]string{"fr": "Congo", "en": "Congo"}, "242", "XAF", "100"},
	{"AIRTEL_COG", "Airtel", "COG", map[string]string{"fr": "Congo", "en": "Congo"}, "242", "XAF", "100"},
	{"AIRTEL_GAB", "Airtel", "GAB", map[string]string{"fr": "Gabon", "en": "Gabon"}, "241", "XAF", "100"},
	{"MTN_MOMO_BEN", "MTN", "BEN", map[string]string{"fr": "Bénin", "en": "Benin"}, "229", "XOF", "100"},
	{"MOOV_BEN", "Moov", "BEN", map[string]string{"fr": "Bénin", "en": "Benin"}, "229", "XOF", "100"},
	{"MTN_MOMO_CIV", "MTN", "CIV", map[string]string{"fr": "Côte d'Ivoire", "en": "Côte d'Ivoire"}, "225", "XOF", "100"},
	{"ORANGE_CIV", "Orange", "CIV", map[string]string{"fr": "Côte d'Ivoire", "en": "Côte d'Ivoire"}, "225", "XOF", "100"},
	{"ORANGE_SEN", "Orange", "SEN", map[string]string{"fr": "Sénégal", "en": "Senegal"}, "221", "XOF", "100"},
	{"FREE_SEN", "Free", "SEN", map[string]string{"fr": "Sénégal", "en": "Senegal"}, "221", "XOF", "100"},
}

var cameroon = map[string]string{"fr": "Cameroun", "en": "Cameroon"}

// OperatorOf: the operator of [provider] in the catalogue.
func OperatorOf(provider string) (Operator, bool) {
	for _, operator := range Operators {
		if operator.Provider == provider {
			return operator, true
		}
	}
	return Operator{}, false
}

// SameValue: whether an amount in [a] is the same amount in [b]: the same
// currency, or the two CFA francs (XAF and XOF, both 655.957 to the euro).
func SameValue(a, b string) bool {
	cfa := func(c string) bool { return c == "XAF" || c == "XOF" }
	return a == b || (cfa(a) && cfa(b))
}
