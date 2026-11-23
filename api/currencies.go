package api

import (
	"context"
	"errors"
	"strings"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
)

// The currency of a payment follows the country of the member's account,
// read from the country code of its phone number (+237: Cameroon). KPAY-NOW
// takes XAF, XOF and USD (no euro): central Africa pays in XAF, west Africa
// in XOF, the rest of the world in USD by card. Mobile money is always paid
// in the operator's currency (XAF for MTN and Orange Cameroon).

// countryOfDialCode: the countries of the CFA francs, by their country code.
var countryOfDialCode = map[string]string{
	// XAF
	"237": "CM", "241": "GA", "242": "CG", "235": "TD", "236": "CF", "240": "GQ",
	// XOF
	"225": "CI", "221": "SN", "229": "BJ", "226": "BF", "223": "ML", "227": "NE", "228": "TG", "245": "GW",
}

// iso3Of: the same countries as the operators name them (CMR).
var iso3Of = map[string]string{
	"CM": "CMR", "GA": "GAB", "CG": "COG", "TD": "TCD", "CF": "CAF", "GQ": "GNQ",
	"CI": "CIV", "SN": "SEN", "BJ": "BEN", "BF": "BFA", "ML": "MLI", "NE": "NER", "TG": "TGO", "GW": "GNB",
}

var xofCountries = map[string]bool{"CI": true, "SN": true, "BJ": true, "BF": true, "ML": true, "NE": true, "TG": true, "GW": true}

// countryOfPhone: the country of an international number (+237 6 70…) when
// it pays in CFA francs; "" for any other country, or a number without its
// « + ».
func countryOfPhone(phone string) string {
	digits := strings.ReplaceAll(strings.TrimSpace(phone), " ", "")
	if !strings.HasPrefix(digits, "+") || len(digits) < 4 {
		return ""
	}
	return countryOfDialCode[digits[1:4]]
}

// cardCurrencyOf: the currency a member of [country] pays in by card.
func cardCurrencyOf(country string) string {
	switch {
	case xofCountries[country]:
		return "XOF"
	case country != "":
		return "XAF"
	default:
		return "USD"
	}
}

// donationRule: the amounts offered for a donation in a currency, and its
// bounds (KPAY takes no card under 1 USD: 1 000 CFA francs at least).
type donationRule struct {
	Currency string  `json:"currency"`
	Amounts  []int64 `json:"amounts"`
	Min      int64   `json:"min"`
	CardMin  int64   `json:"card_min"`
	Max      int64   `json:"max"`
}

// cardMinimum: the least a card pays in [currency] (0 when not known).
func cardMinimum(currency string) int64 {
	return donationRules[currency].CardMin
}

// minFor: the least that can be given by [method].
func (r donationRule) minFor(method string) int64 {
	if method == methodCard {
		return r.CardMin
	}
	return r.Min
}

var donationRules = map[string]donationRule{
	"XAF": {Currency: "XAF", Amounts: []int64{1000, 2000, 5000, 10000}, Min: minDonation, CardMin: 1000, Max: maxDonation},
	"XOF": {Currency: "XOF", Amounts: []int64{1000, 2000, 5000, 10000}, Min: minDonation, CardMin: 1000, Max: maxDonation},
	"USD": {Currency: "USD", Amounts: []int64{5, 10, 25, 50}, Min: 1, CardMin: 1, Max: 1000},
}

// errCurrencyMismatch: the currency shown to the member is not the one the
// payment would be made in (409 CURRENCY_MISMATCH).
var errCurrencyMismatch = errors.New("the currency is not the one of this payment")

// accountCountry: the country of [userID]'s number ("" when unknown).
func (s *Server) accountCountry(ctx context.Context, userID int64) string {
	user, err := s.store.GetUserById(ctx, userID)
	if err != nil {
		return ""
	}
	return countryOfPhone(user.PhoneNumber)
}

// otherCurrencies: whether the provider takes more than XAF (KPAY-NOW);
// with Wajub everything stays in XAF.
func (s *Server) otherCurrencies() bool {
	return s.config.Pay.Provider == utils.ProviderKPay
}

// cardCurrency: the currency a member of [country] pays in by card.
func (s *Server) cardCurrency(country string) string {
	if !s.otherCurrencies() {
		return "XAF"
	}
	return cardCurrencyOf(country)
}

// preferredMethod: mobile money where an operator of [country] is offered,
// else the card (when cards are taken); mobile money for everyone with
// Wajub, as before.
func (s *Server) preferredMethod(country string) string {
	if !s.otherCurrencies() || !s.config.Pay.Cards() {
		return methodMobileMoney
	}
	for _, operator := range s.operators() {
		if iso3 := iso3Of[country]; iso3 != "" && operator.Country == iso3 {
			return methodMobileMoney
		}
	}
	return methodCard
}

// chargeCurrency: the currency of a payment by [method]: the operator's
// (XAF) for mobile money, the one of the account's country for a card.
// [asked] is the currency the member was shown: none (the versions before
// 18/11/2026) is XAF, as before; another than the payment's is refused.
func (s *Server) chargeCurrency(ctx context.Context, userID int64, method, asked string) (string, error) {
	asked = strings.ToUpper(strings.TrimSpace(asked))
	want := "XAF"
	if asked != "" && method == methodCard {
		want = s.cardCurrency(s.accountCountry(ctx, userID))
	}
	if asked != "" && asked != want {
		return "", errCurrencyMismatch
	}
	return want, nil
}

// donationRulesFor: the currencies a member of [country] can give in: XAF
// (mobile money), and the one of their card.
func (s *Server) donationRulesFor(country string) []donationRule {
	rules := []donationRule{donationRules["XAF"]}
	if card := s.cardCurrency(country); card != "XAF" {
		rules = append(rules, donationRules[card])
	}
	return rules
}
