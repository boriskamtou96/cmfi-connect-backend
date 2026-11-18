package api

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"testing"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestTheCountryOfANumberGivesItsCardCurrency(t *testing.T) {
	for phone, want := range map[string][2]string{
		"+237670000000":      {"CM", "XAF"},
		"+237 6 70 00 00 00": {"CM", "XAF"},
		"+241062000000":      {"GA", "XAF"},
		"+2250700000000":     {"CI", "XOF"},
		"+221770000000":      {"SN", "XOF"},
		"+33612345678":       {"", "USD"},
		"+15145550000":       {"", "USD"},
		"":                   {"", "USD"},
		"237670000000":       {"", "USD"},
	} {
		country := countryOfPhone(phone)
		require.Equal(t, want[0], country, phone)
		require.Equal(t, want[1], cardCurrencyOf(country), phone)
	}
}

func TestPremiumPricesFollowTheCurrency(t *testing.T) {
	pay := utils.PaymentsConfig{
		PremiumPrices:    map[string]int64{"monthly": 1500},
		PremiumPricesUSD: map[string]int64{"monthly": 5},
	}
	require.Equal(t, int64(1500), pay.PremiumPricesIn("XAF")["monthly"])
	// without prices of their own, XOF are those of XAF (the same value)
	require.Equal(t, int64(1500), pay.PremiumPricesIn("XOF")["monthly"])
	require.Equal(t, int64(5), pay.PremiumPricesIn("USD")["monthly"])
	require.Nil(t, pay.PremiumPricesIn("EUR"))
}

// kpayWorld: the payments world with KPAY-NOW, which takes XOF and USD.
func kpayWorld(t *testing.T) paymentsWorld {
	w := newPaymentsWorld(t)
	w.server.config.Pay.Provider = utils.ProviderKPay
	w.server.config.Pay.KPayCards = true
	w.server.config.Pay.PremiumPricesUSD = map[string]int64{"monthly": 5, "six_month": 25, "yearly": 50, "lifetime": 100}
	return w
}

// abroad: a member whose number is French.
func abroad(t *testing.T, w paymentsWorld) member {
	m := registerMember(t, w.router, nil)
	runSQL(t, `UPDATE users SET phone_number = $1 WHERE id = $2`, fmt.Sprintf("+336%08d", rand.IntN(100_000_000)), m.ID)
	return m
}

type paymentChoices struct {
	Country      string         `json:"country"`
	CardCurrency string         `json:"card_currency"`
	Preferred    string         `json:"preferred"`
	Donations    []donationRule `json:"donations"`
}

func currenciesOf(rules []donationRule) []string {
	found := []string{}
	for _, rule := range rules {
		found = append(found, rule.Currency)
	}
	return found
}

func TestADonationByCardIsInTheCurrencyOfTheAccountsCountry(t *testing.T) {
	w := kpayWorld(t)
	m := abroad(t, w)

	choices := decode[paymentChoices](t, send(t, w.router, http.MethodGet, m.Token, "/v1/payments/providers", nil))
	require.Equal(t, "", choices.Country)
	require.Equal(t, "USD", choices.CardCurrency)
	require.Equal(t, "card", choices.Preferred)
	require.ElementsMatch(t, []string{"XAF", "USD"}, currenciesOf(choices.Donations))

	gift := w.pay(t, m.Token, "/v1/donations", map[string]any{"amount": 10, "currency": "USD", "method": "card"})
	require.Equal(t, "USD", gift.Currency)
	require.Equal(t, int64(10), gift.Amount)
	require.Equal(t, "USD", w.money.inits[gift.ID].Currency)

	mismatch := send(t, w.router, http.MethodPost, m.Token, "/v1/donations", map[string]any{"amount": 10, "currency": "XAF", "method": "card"})
	require.Equal(t, http.StatusConflict, mismatch.Code, mismatch.Body.String())
	require.Equal(t, "CURRENCY_MISMATCH", errorCode(t, mismatch))
	tooMuch := send(t, w.router, http.MethodPost, m.Token, "/v1/donations", map[string]any{"amount": 2000, "currency": "USD", "method": "card"})
	require.Equal(t, http.StatusConflict, tooMuch.Code, tooMuch.Body.String())
	require.Equal(t, "AMOUNT_OUT_OF_BOUNDS", errorCode(t, tooMuch))

	// in Cameroon, mobile money comes first, in CFA francs
	home := registerMember(t, w.router, nil)
	choices = decode[paymentChoices](t, send(t, w.router, http.MethodGet, home.Token, "/v1/payments/providers", nil))
	require.Equal(t, "CM", choices.Country)
	require.Equal(t, "XAF", choices.CardCurrency)
	require.Equal(t, "mobile_money", choices.Preferred)
	require.Equal(t, []string{"XAF"}, currenciesOf(choices.Donations))
}

func TestMobileMoneyStaysInTheOperatorsCurrency(t *testing.T) {
	w := kpayWorld(t)
	m := abroad(t, w)

	gift := w.pay(t, m.Token, "/v1/donations", map[string]any{"amount": 1000, "currency": "XAF", "phone_number": "+237670000009"})
	require.Equal(t, "XAF", gift.Currency)
	mismatch := send(t, w.router, http.MethodPost, m.Token, "/v1/donations", map[string]any{"amount": 10, "currency": "USD", "phone_number": "+237670000009"})
	require.Equal(t, http.StatusConflict, mismatch.Code, mismatch.Body.String())
	require.Equal(t, "CURRENCY_MISMATCH", errorCode(t, mismatch))
}

func TestAnOldAppWithoutACurrencyStillPaysInXAF(t *testing.T) {
	w := kpayWorld(t)
	m := abroad(t, w)

	gift := w.pay(t, m.Token, "/v1/donations", map[string]any{"amount": 2500, "method": "card"})
	require.Equal(t, "XAF", gift.Currency)
	require.Equal(t, int64(2500), gift.Amount)
	// KPAY takes no card under 1 USD
	tooLittle := send(t, w.router, http.MethodPost, m.Token, "/v1/donations", map[string]any{"amount": 500, "method": "card"})
	require.Equal(t, http.StatusConflict, tooLittle.Code, tooLittle.Body.String())
	require.Equal(t, "AMOUNT_OUT_OF_BOUNDS", errorCode(t, tooLittle))
}

func TestPremiumByCardIsPricedInTheAccountsCurrency(t *testing.T) {
	w := kpayWorld(t)
	m := abroad(t, w)

	plans := decode[struct {
		Prices map[string][]premiumPlanResponse `json:"prices"`
	}](t, send(t, w.router, http.MethodGet, "", "/v1/premium/plans", nil))
	require.Contains(t, plans.Prices["USD"], premiumPlanResponse{Plan: "monthly", Amount: 5})
	require.Contains(t, plans.Prices["XAF"], premiumPlanResponse{Plan: "monthly", Amount: 1500})
	require.Contains(t, plans.Prices["XOF"], premiumPlanResponse{Plan: "monthly", Amount: 1500})

	status := decode[paymentChoices](t, send(t, w.router, http.MethodGet, m.Token, "/v1/premium/status", nil))
	require.Equal(t, "USD", status.CardCurrency)
	require.Equal(t, "card", status.Preferred)

	bought := w.pay(t, m.Token, "/v1/premium/payments", map[string]any{"plan": "monthly", "method": "card", "currency": "USD"})
	require.Equal(t, "USD", bought.Currency)
	require.Equal(t, int64(5), bought.Amount)
}

func TestWithWajubEverythingStaysInXAF(t *testing.T) {
	w := newPaymentsWorld(t)
	m := abroad(t, w)

	choices := decode[paymentChoices](t, send(t, w.router, http.MethodGet, m.Token, "/v1/payments/providers", nil))
	require.Equal(t, "XAF", choices.CardCurrency)
	// nothing changes with Wajub: mobile money first, for everyone
	require.Equal(t, "mobile_money", choices.Preferred)
	require.Equal(t, []string{"XAF"}, currenciesOf(choices.Donations))
	plans := decode[struct {
		Prices map[string][]premiumPlanResponse `json:"prices"`
	}](t, send(t, w.router, http.MethodGet, "", "/v1/premium/plans", nil))
	require.Len(t, plans.Prices, 1)
	require.NotEmpty(t, plans.Prices["XAF"])

	gift := w.pay(t, m.Token, "/v1/donations", map[string]any{"amount": 2000, "currency": "XAF", "method": "card"})
	require.Equal(t, "XAF", gift.Currency)
}
