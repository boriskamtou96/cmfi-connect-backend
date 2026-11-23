package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type operatorsAnswer struct {
	Countries []countryResponse `json:"countries"`
	CardMin   int64             `json:"card_min"`
}

func countriesOf(answer operatorsAnswer) []string {
	found := []string{}
	for _, country := range answer.Countries {
		found = append(found, country.Country)
	}
	return found
}

func TestKPayOffersMobileMoneyInTheSixCountriesOfTheCFAFrancs(t *testing.T) {
	w := kpayWorld(t)
	m := registerMember(t, w.router, nil)

	answer := decode[operatorsAnswer](t, send(t, w.router, http.MethodGet, m.Token, "/v1/payments/providers?currency=XAF", nil))
	// an event in XAF is paid as well in XOF, the same amount
	require.Equal(t, []string{"CMR", "COG", "GAB", "BEN", "CIV", "SEN"}, countriesOf(answer))
	require.Equal(t, int64(1000), answer.CardMin)

	// KPAY_PROVIDERS still narrows it
	w.server.config.Pay.KPayProviders = []string{"ORANGE_SEN"}
	answer = decode[operatorsAnswer](t, send(t, w.router, http.MethodGet, m.Token, "/v1/payments/providers?currency=XOF", nil))
	require.Equal(t, []string{"SEN"}, countriesOf(answer))

	// Wajub only serves Cameroon
	wajub := newPaymentsWorld(t)
	other := registerMember(t, wajub.router, nil)
	answer = decode[operatorsAnswer](t, send(t, wajub.router, http.MethodGet, other.Token, "/v1/payments/providers", nil))
	require.Equal(t, []string{"CMR"}, countriesOf(answer))
}

func TestASenegaleseMemberPaysAnEventInXAFWithItsXOF(t *testing.T) {
	w := kpayWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 1500})

	payment := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{"phone_number": "+221770000001"})
	require.Equal(t, "ORANGE_SEN", payment.Provider)
	require.Equal(t, "XOF", payment.Currency)
	require.Equal(t, int64(1500), payment.Amount)
	asked := w.money.inits[payment.ID]
	require.Equal(t, "XOF", asked.Currency)
	require.Equal(t, int64(1500), asked.Amount)

	// Cameroon's stays in XAF
	home := registerMember(t, w.router, nil)
	payment = w.pay(t, home.Token, eventPath(event, "/payments"), map[string]any{"phone_number": "+237670000031"})
	require.Equal(t, "XAF", payment.Currency)
}

func TestACardUnderItsMinimumIsRefusedBeforeTheProviderIsAsked(t *testing.T) {
	w := kpayWorld(t)
	admin := staffAdmin(t, w.router)
	m := registerMember(t, w.router, nil)
	cheap := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 300})

	refused := send(t, w.router, http.MethodPost, m.Token, eventPath(cheap, "/payments"), map[string]any{"method": "card"})
	require.Equal(t, http.StatusConflict, refused.Code, refused.Body.String())
	require.Equal(t, "CARD_MINIMUM", errorCode(t, refused))
	require.Empty(t, w.money.inits)

	// four places reach it
	twice := w.pay(t, m.Token, eventPath(cheap, "/payments"), map[string]any{"method": "card", "people": 4})
	require.Equal(t, int64(1200), twice.Amount)
}

func TestAPayoutOnlyForwardsTheMoneyOfItsCountrysWallet(t *testing.T) {
	w := newPaymentsWorld(t)
	admin := staffAdmin(t, w.router)
	event := createEvent(t, w.router, admin.Token, map[string]any{"price_amount": 5000})
	ids := []string{}
	for _, phone := range []string{"+237670000041", "+237670000042"} {
		m := registerMember(t, w.router, nil)
		payment := w.pay(t, m.Token, eventPath(event, "/payments"), map[string]any{"phone_number": phone})
		w.notify(t, "payment.completed", payment.ID, map[string]any{"amount": 5000})
		ids = append(ids, payment.ID)
	}
	// one was paid from Senegal: its money is in Senegal's wallet
	runSQL(t, `UPDATE payments SET provider = 'ORANGE_SEN', currency = 'XOF' WHERE id = $1`, ids[1])

	overview := w.overview(t, admin.Token, event)
	require.Equal(t, toForwardResponse{Payments: 1, Amount: 5000, Net: 4750, Currency: "XAF"}, overview.ToForward)
}
