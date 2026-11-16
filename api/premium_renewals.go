package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/push"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Premium renewed by itself (16/11/2026). Mobile money can't be taken
// without the member's PIN: chosen when paying (« Renouveler
// automatiquement »), the server sends the payment request of the same plan
// to the same number the day before the end, at 10:00 in Yaoundé; the
// member types the PIN. Unanswered or refused, it asks again the next day,
// 3 times at most (the day before, the day of the end, the day after); then
// Premium ends and the member is told. Stopped at any time.

const (
	// the requests for one end of Premium
	maxRenewalAttempts = 3
	// a renewal is looked at from this long before the end of Premium
	renewalLookahead = 36 * time.Hour
)

// renewalZone: the hours of the requests are those of Yaoundé (no summer
// time: a fixed zone, whatever the tz database of the server).
var renewalZone = time.FixedZone("WAT", 3600)

// renewalTime: when the request number [attempts]+1 for the Premium ending
// at [end] goes: the day before the end at 10:00 in Yaoundé, then a day after
// the last one; always between 8:00 and 20:00 there, never at night.
func renewalTime(end time.Time, attempts int32, last sql.NullTime) time.Time {
	local := end.In(renewalZone)
	at := time.Date(local.Year(), local.Month(), local.Day()-1, 10, 0, 0, 0, renewalZone)
	if attempts > 0 && last.Valid && last.Time.Add(23*time.Hour).After(at) {
		at = last.Time.Add(23 * time.Hour).In(renewalZone)
	}
	switch {
	case at.Hour() < 8:
		at = time.Date(at.Year(), at.Month(), at.Day(), 8, 0, 0, 0, renewalZone)
	case at.Hour() >= 20:
		at = time.Date(at.Year(), at.Month(), at.Day()+1, 8, 0, 0, 0, renewalZone)
	}
	return at
}

type premiumRenewalResponse struct {
	Plan        string `json:"plan"`
	PhoneNumber string `json:"phone_number"`
	Provider    string `json:"provider"`
	// when the next payment request goes
	NextRequestAt time.Time `json:"next_request_at"`
}

// premiumRenewalOf: the renewal of [userID], nil when there is none.
func (s *Server) premiumRenewalOf(ctx context.Context, userID int64) (*premiumRenewalResponse, error) {
	renewal, err := s.store.GetPremiumRenewal(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &premiumRenewalResponse{
		Plan:          renewal.Plan,
		PhoneNumber:   "+" + strings.TrimPrefix(renewal.PhoneNumber, "+"),
		Provider:      renewal.Provider,
		NextRequestAt: renewalTime(renewal.PeriodEnd, renewal.Attempts, renewal.LastAttemptAt).UTC(),
	}, nil
}

// DELETE /v1/premium/renewal: no more requests; Premium goes on to its end.
func (s *Server) stopPremiumRenewal(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if _, err := s.store.DeletePremiumRenewal(c, authUser.ID); err != nil {
		internalServerError(c, err)
		return
	}
	logFor(c).Info("premium renewal stopped", "user_id", authUser.ID)
	c.Status(http.StatusNoContent)
}

// keepPremiumRenewal: a paid plan asked to be renewed ([payment].Renew):
// its number and operator are kept for the requests before [until].
func (s *Server) keepPremiumRenewal(ctx context.Context, payment db.Payment, until time.Time) {
	if !payment.Renew || !payment.UserID.Valid || payment.PhoneNumber == "" || payment.Provider == cardProvider {
		return
	}
	if _, err := s.store.SavePremiumRenewal(ctx, db.SavePremiumRenewalParams{
		UserID:      payment.UserID.Int64,
		Plan:        payment.PremiumPlan,
		PhoneNumber: payment.PhoneNumber,
		Provider:    payment.Provider,
		PeriodEnd:   until,
	}); err != nil {
		slog.Error("cannot keep a premium renewal", "payment", payment.ID, "error", err)
	}
}

// renewPremiums sends the payment requests that are due (the reconciler,
// every minute).
func (s *Server) renewPremiums(ctx context.Context, now time.Time) {
	renewals, err := s.store.ListDuePremiumRenewals(ctx, now.Add(renewalLookahead))
	if err != nil {
		slog.Error("cannot list the premium renewals", "error", err)
		return
	}
	for _, renewal := range renewals {
		s.renewPremium(ctx, renewal, now)
	}
}

func (s *Server) renewPremium(ctx context.Context, renewal db.PremiumRenewal, now time.Time) {
	log := slog.Default().With("user_id", renewal.UserID)
	lifetime, until, err := s.premiumBought(ctx, renewal.UserID)
	if err != nil {
		log.Error("cannot read the premium of a member to renew", "error", err)
		return
	}
	if lifetime {
		_, _ = s.store.DeletePremiumRenewal(ctx, renewal.UserID)
		return
	}
	// a new end (renewed, paid by hand, offered): its requests come later
	if until != nil && until.After(renewal.PeriodEnd.Add(time.Minute)) {
		if err := s.store.RestartPremiumRenewal(ctx, db.RestartPremiumRenewalParams{
			UserID: renewal.UserID, PeriodEnd: *until,
		}); err != nil {
			log.Error("cannot restart a premium renewal", "error", err)
		}
		return
	}
	// the last request: still awaited, or paid and giving Premium now
	if renewal.LastPaymentID.Valid {
		last, err := s.store.GetPayment(ctx, renewal.LastPaymentID.UUID)
		if err != nil {
			log.Error("cannot read the last renewal request", "error", err)
			return
		}
		if last.Status != "failed" {
			return
		}
	}
	if renewal.Attempts >= maxRenewalAttempts {
		// refused or unanswered each time: Premium ends
		if _, err := s.store.DeletePremiumRenewal(ctx, renewal.UserID); err != nil {
			log.Error("cannot stop a premium renewal", "error", err)
			return
		}
		log.Info("premium renewal given up", "attempts", renewal.Attempts)
		s.pushRenewal(renewal.UserID, renewalFailedTitles, renewalFailedBodies, func(body string) string { return body })
		return
	}
	if now.Before(renewalTime(renewal.PeriodEnd, renewal.Attempts, renewal.LastAttemptAt)) {
		return
	}
	price, ok := s.config.Pay.PremiumPrices[renewal.Plan]
	if !ok {
		log.Warn("premium renewal stopped: the plan is no longer sold", "plan", renewal.Plan)
		_, _ = s.store.DeletePremiumRenewal(ctx, renewal.UserID)
		return
	}
	payment, err := s.store.CreatePayment(ctx, db.CreatePaymentParams{
		ID:          uuid.New(),
		UserID:      nullID(renewal.UserID),
		Purpose:     paymentForPremium,
		People:      1,
		PremiumPlan: renewal.Plan,
		Amount:      price,
		Currency:    s.config.Pay.PremiumCurrency,
		PhoneNumber: renewal.PhoneNumber,
		Provider:    renewal.Provider,
		Renew:       true,
		Renewal:     true,
	})
	if err != nil {
		log.Error("cannot keep a renewal request", "error", err)
		return
	}
	// counted before it is sent: a crash never sends it twice
	if err := s.store.RecordPremiumRenewalAttempt(ctx, db.RecordPremiumRenewalAttemptParams{
		UserID: renewal.UserID, LastPaymentID: uuid.NullUUID{UUID: payment.ID, Valid: true},
	}); err != nil {
		log.Error("cannot count a renewal request", "error", err)
		s.failPayment(ctx, payment.ID, "PAYMENTS_UNAVAILABLE")
		return
	}
	if err := s.askMobileMoney(ctx, log, payment, paymentStatementText+" Premium"); err != nil {
		log.Error("cannot keep the answer to a renewal request", "payment", payment.ID, "error", err)
	}
	if payment, err = s.store.GetPayment(ctx, payment.ID); err != nil || payment.Status == "failed" {
		log.Warn("renewal request refused", "payment", payment.ID, "failure", payment.FailureCode)
		return
	}
	log.Info("renewal request sent", "payment", payment.ID, "attempt", renewal.Attempts+1)
	s.pushRenewal(renewal.UserID, renewalAskedTitles, renewalAskedBodies,
		func(pattern string) string { return fmt.Sprintf(pattern, moneyText(price, payment.Currency)) })
}

// pushRenewed: the request was paid, Premium goes on until [until].
func (s *Server) pushRenewed(userID int64, until time.Time) {
	date := until.In(renewalZone).Format("02/01/2006")
	s.pushRenewal(userID, renewedTitles, renewedBodies,
		func(pattern string) string { return fmt.Sprintf(pattern, date) })
}

// pushRenewal: [titles] and [bodies] in the language of each phone of
// [userID]; [fill] writes the body.
func (s *Server) pushRenewal(userID int64, titles, bodies map[string]string, fill func(string) string) {
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	devices, err := s.store.ListDevicesOfUsers(ctx, []int64{userID})
	if err != nil {
		slog.Error("cannot read the phones for a renewal push", "user_id", userID, "error", err)
		return
	}
	byLanguage := map[string][]string{}
	for _, device := range devices {
		byLanguage[device.Language] = append(byLanguage[device.Language], device.Token)
	}
	dead := []string{}
	for language, tokens := range byLanguage {
		result, err := s.push.Send(ctx, tokens, push.Message{
			Title: titleIn(titles, language, ""),
			Body:  fill(renewalText(bodies, language)),
			Data:  map[string]string{"type": "premium_renewal"},
		})
		if err != nil {
			slog.Error("renewal push failed", "user_id", userID, "language", language, "error", err)
		}
		dead = append(dead, result.Dead...)
	}
	if len(dead) > 0 {
		_ = s.store.DeleteDevicesByTokens(ctx, dead)
	}
}

// renewalText: [texts] in [language] (« fr-CM » → fr), French otherwise; its %s
// left for the caller.
func renewalText(texts map[string]string, language string) string {
	base, _, _ := strings.Cut(strings.ToLower(language), "-")
	if text, ok := texts[base]; ok {
		return text
	}
	return texts["fr"]
}

// moneyText: 1500 XAF → « 1 500 FCFA ».
func moneyText(amount int64, currency string) string {
	digits := strconv.FormatInt(amount, 10)
	var grouped strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			grouped.WriteRune(' ')
		}
		grouped.WriteRune(digit)
	}
	if currency == "XAF" {
		currency = "FCFA"
	}
	return grouped.String() + " " + currency
}

var renewalAskedTitles = map[string]string{
	"fr": "Renouvelle ton Premium",
	"en": "Renew your Premium",
	"de": "Verlängere dein Premium",
	"es": "Renueva tu Premium",
	"ar": "جدّد اشتراك Premium",
	"zh": "续订你的 Premium",
	"hi": "अपना Premium नवीनीकृत करें",
}

var renewalAskedBodies = map[string]string{
	"fr": "Une demande de %s arrive sur ton téléphone : saisis ton code mobile money pour continuer Premium.",
	"en": "A request for %s is coming to your phone: enter your mobile money PIN to keep Premium.",
	"de": "Eine Zahlungsanfrage über %s kommt auf dein Handy: Gib deine Mobile-Money-PIN ein, um Premium zu behalten.",
	"es": "Una solicitud de %s llega a tu teléfono: introduce tu PIN de mobile money para seguir con Premium.",
	"ar": "يصل إلى هاتفك طلب دفع بقيمة %s: أدخل رمز mobile money لمتابعة Premium.",
	"zh": "一笔 %s 的付款请求将发到你的手机：输入你的 mobile money 密码以继续使用 Premium。",
	"hi": "%s का भुगतान अनुरोध आपके फ़ोन पर आ रहा है: Premium जारी रखने के लिए अपना mobile money PIN डालें।",
}

var renewedTitles = map[string]string{
	"fr": "Premium renouvelé",
	"en": "Premium renewed",
	"de": "Premium verlängert",
	"es": "Premium renovado",
	"ar": "تم تجديد Premium",
	"zh": "Premium 已续订",
	"hi": "Premium नवीनीकृत हुआ",
}

var renewedBodies = map[string]string{
	"fr": "Merci ! Premium continue jusqu'au %s. Que Dieu te bénisse !",
	"en": "Thank you! Premium goes on until %s. God bless you!",
	"de": "Danke! Premium läuft bis zum %s weiter. Gott segne dich!",
	"es": "¡Gracias! Premium sigue hasta el %s. ¡Dios te bendiga!",
	"ar": "شكرًا! يستمر Premium حتى %s. ليباركك الله!",
	"zh": "谢谢！Premium 将持续到 %s。愿神赐福你！",
	"hi": "धन्यवाद! Premium %s तक जारी है। परमेश्वर आपको आशीष दे!",
}

var renewalFailedTitles = map[string]string{
	"fr": "Premium n'a pas pu être renouvelé",
	"en": "Premium could not be renewed",
	"de": "Premium konnte nicht verlängert werden",
	"es": "No se pudo renovar Premium",
	"ar": "تعذّر تجديد Premium",
	"zh": "Premium 未能续订",
	"hi": "Premium नवीनीकृत नहीं हो सका",
}

var renewalFailedBodies = map[string]string{
	"fr": "Les demandes de paiement sont restées sans réponse. Tu peux reprendre Premium quand tu veux depuis l'app.",
	"en": "The payment requests got no answer. You can get Premium again whenever you like from the app.",
	"de": "Die Zahlungsanfragen blieben unbeantwortet. Du kannst Premium jederzeit in der App erneut holen.",
	"es": "Las solicitudes de pago quedaron sin respuesta. Puedes volver a obtener Premium cuando quieras desde la app.",
	"ar": "بقيت طلبات الدفع بلا رد. يمكنك الحصول على Premium من جديد متى شئت من التطبيق.",
	"zh": "付款请求未得到回应。你可以随时在应用中重新开通 Premium。",
	"hi": "भुगतान अनुरोधों का कोई जवाब नहीं मिला। आप ऐप से जब चाहें Premium फिर से ले सकते हैं।",
}
