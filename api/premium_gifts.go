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
	"strconv"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/push"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// Premium offered by the admins: 1 to 3 months, to one member (by their
// pseudo) or to a group (the places of the notifications: a country, a
// city, a church, an assembly; nothing: everyone). Each member's gift comes
// after their current Premium (the one bought on the website, the gifts
// before), one at a time in the background, and the member is told by a
// push. An offer can be taken back. All of it is kept here: the apps read it
// (GET /v1/premium/status).

var premiumGiftTitles = map[string]string{
	"fr": "Premium t'est offert",
	"en": "Premium is on us",
	"de": "Premium ist ein Geschenk für dich",
	"es": "Premium es un regalo para ti",
	"ar": "Premium هدية لك",
	"zh": "送你 Premium",
	"hi": "आपके लिए Premium उपहार",
}

var premiumGiftBodies = map[string]string{
	"fr": "Profite de CMFI Connect Premium pendant %d mois. Que Dieu te bénisse !",
	"en": "Enjoy CMFI Connect Premium for %d months. God bless you!",
	"de": "Genieße CMFI Connect Premium für %d Monate. Gott segne dich!",
	"es": "Disfruta de CMFI Connect Premium durante %d meses. ¡Dios te bendiga!",
	"ar": "استمتع بـ CMFI Connect Premium لمدة %d أشهر. ليباركك الله!",
	"zh": "尽享 CMFI Connect Premium %d 个月。愿神赐福你！",
	"hi": "%d महीने CMFI Connect Premium का आनंद लें। परमेश्वर आपको आशीष दे!",
}

type premiumOfferRequest struct {
	// 1, 2 or 3
	Months int32 `json:"months" binding:"required"`
	// one member; else the group of [Audience]
	Username string         `json:"username"`
	Audience audienceFilter `json:"audience"`
	Note     string         `json:"note"`
	// tell the members by a push (true when absent)
	Notify *bool `json:"notify"`
}

type premiumOfferAudience struct {
	Username string `json:"username,omitempty"`
	audienceFilter
}

type premiumOfferResponse struct {
	ID           int64           `json:"id"`
	Months       int32           `json:"months"`
	Audience     json.RawMessage `json:"audience"`
	Note         string          `json:"note"`
	Notify       bool            `json:"notify"`
	MembersCount int32           `json:"members_count"`
	Given        int32           `json:"given"`
	Pending      int32           `json:"pending"`
	Failed       int32           `json:"failed"`
	CreatedAt    time.Time       `json:"created_at"`
	RevokedAt    *time.Time      `json:"revoked_at"`
	CreatedBy    *memberCard     `json:"created_by"`
}

// POST /v1/admin/premium/offers: Premium offered to a member or a group.
func (s *Server) offerPremium(c *gin.Context) {
	var req premiumOfferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if req.Months < 1 || req.Months > 3 {
		badRequestError(c, fmt.Errorf("months: 1 to 3"))
		return
	}
	audience := premiumOfferAudience{Username: strings.TrimPrefix(strings.TrimSpace(req.Username), "@")}
	var members []int64
	if audience.Username != "" {
		user, err := s.store.GetUserByUsername(c, strings.ToLower(audience.Username))
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, NewErrorResponse(http.StatusNotFound, "USERNAME_NOT_FOUND", err))
			return
		}
		if err != nil {
			internalServerError(c, err)
			return
		}
		members = []int64{user.ID}
	} else {
		audience.audienceFilter = req.Audience.trimmed()
		_, ids, err := s.audienceOf(c, audience.audienceFilter)
		if err != nil {
			internalServerError(c, err)
			return
		}
		members = ids
	}
	if len(members) == 0 {
		c.JSON(http.StatusBadRequest, NewErrorResponse(http.StatusBadRequest, "NO_RECIPIENTS",
			errors.New("no member matches this audience")))
		return
	}

	rawAudience, _ := json.Marshal(audience)
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	offer, err := s.store.CreatePremiumOffer(c, db.CreatePremiumOfferParams{
		Months:       req.Months,
		Audience:     rawAudience,
		Note:         strings.TrimSpace(req.Note),
		Notify:       req.Notify == nil || *req.Notify,
		MembersCount: int32(len(members)),
		CreatedBy:    nullID(authUser.ID),
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if err := s.store.CreatePremiumGifts(c, db.CreatePremiumGiftsParams{
		OfferID: offer.ID, UserIds: members, Months: req.Months,
	}); err != nil {
		internalServerError(c, err)
		return
	}
	logFor(c).Info("premium offered", "offer", offer.ID, "months", offer.Months, "members", len(members))

	// one member: given at once; a group: in the background
	if len(members) == 1 {
		s.giveGifts(c, sql.NullInt64{Int64: offer.ID, Valid: true}, 1)
	} else {
		s.gifting.Add(1)
		go func() {
			defer s.gifting.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			for s.giveGifts(ctx, sql.NullInt64{Int64: offer.ID, Valid: true}, 50) > 0 {
			}
		}()
	}
	s.sendPremiumOffer(c, http.StatusCreated, offer.ID)
}

// giveGifts gives up to [limit] pending gifts (of [offer] when set) and says
// how many it tried.
func (s *Server) giveGifts(ctx context.Context, offer sql.NullInt64, limit int32) int {
	gifts, err := s.store.ListPendingGifts(ctx, db.ListPendingGiftsParams{OfferID: offer, Lim: limit})
	if err != nil {
		slog.Error("cannot list the premium gifts to give", "error", err)
		return 0
	}
	for _, gift := range gifts {
		s.giveGift(ctx, gift)
	}
	return len(gifts)
}

func (s *Server) giveGift(ctx context.Context, gift db.PremiumGift) {
	lifetime, current, err := s.premiumBought(ctx, gift.UserID)
	if err != nil {
		slog.Error("cannot read the premium of a member", "gift", gift.ID, "error", err)
		return
	}
	var until *time.Time
	if !lifetime {
		from := time.Now()
		if current != nil && current.After(from) {
			from = *current
		}
		end := from.AddDate(0, int(gift.Months), 0)
		until = &end
	}
	given := db.GiveGiftParams{ID: gift.ID}
	if until != nil {
		given.Until = sql.NullTime{Time: *until, Valid: true}
	}
	if err := s.store.GiveGift(ctx, given); err != nil {
		slog.Error("cannot mark a gift given", "gift", gift.ID, "error", err)
		return
	}
	if offer, err := s.store.GetPremiumOffer(ctx, gift.OfferID); err == nil && offer.Notify {
		var audience struct {
			Streak int `json:"streak"`
		}
		_ = json.Unmarshal(offer.Audience, &audience)
		s.pushPremiumGift(gift, audience.Streak)
	}
}

// pushPremiumGift tells the member, in the language of each phone; [streak]:
// the days in a row it rewards (0: offered by the admins).
func (s *Server) pushPremiumGift(gift db.PremiumGift, streak int) {
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	devices, err := s.store.ListDevicesOfUsers(ctx, []int64{gift.UserID})
	if err != nil || len(devices) == 0 {
		return
	}
	byLanguage := map[string][]string{}
	for _, device := range devices {
		byLanguage[device.Language] = append(byLanguage[device.Language], device.Token)
	}
	dead := []string{}
	for language, tokens := range byLanguage {
		title := titleIn(premiumGiftTitles, language, "")
		body := fmt.Sprintf(titleIn(premiumGiftBodies, language, ""), gift.Months)
		if streak > 0 {
			title = fmt.Sprintf(titleIn(streakGiftTitles, language, ""), streak)
			body = titleIn(streakGiftBodies, language, "")
		}
		result, err := s.push.Send(ctx, tokens, push.Message{
			Title: title,
			Body:  body,
			Data:  map[string]string{"type": "premium_gift", "id": strconv.FormatInt(gift.OfferID, 10)},
		})
		if err != nil {
			slog.Error("push of a premium gift failed", "gift", gift.ID, "error", err)
		}
		dead = append(dead, result.Dead...)
	}
	if len(dead) > 0 {
		_ = s.store.DeleteDevicesByTokens(ctx, dead)
	}
}

// GET /v1/admin/premium/offers?page=&size=: latest first.
func (s *Server) adminListPremiumOffers(c *gin.Context) {
	var req pageRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	limit, offset := req.limits()
	rows, err := s.store.ListPremiumOffers(c, db.ListPremiumOffersParams{Lim: limit, Off: offset})
	if err != nil {
		internalServerError(c, err)
		return
	}
	responses, err := s.premiumOfferResponses(c, rows)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, responses)
}

func (s *Server) premiumOfferResponses(c *gin.Context, rows []db.ListPremiumOffersRow) ([]premiumOfferResponse, error) {
	ids := []int64{}
	for _, row := range rows {
		if row.CreatedBy.Valid {
			ids = append(ids, row.CreatedBy.Int64)
		}
	}
	cards := map[int64]memberCard{}
	if len(ids) > 0 {
		found, err := s.store.ListMemberCards(c, ids)
		if err != nil {
			return nil, err
		}
		for _, card := range found {
			cards[card.ID] = toMemberCard(card)
		}
	}
	responses := make([]premiumOfferResponse, 0, len(rows))
	for _, row := range rows {
		response := premiumOfferResponse{
			ID: row.ID, Months: row.Months, Audience: row.Audience, Note: row.Note, Notify: row.Notify,
			MembersCount: row.MembersCount, Given: row.Given, Pending: row.Pending, Failed: row.Failed,
			CreatedAt: row.CreatedAt, RevokedAt: timePtr(row.RevokedAt),
		}
		if card, ok := cards[row.CreatedBy.Int64]; ok {
			response.CreatedBy = &card
		}
		responses = append(responses, response)
	}
	return responses, nil
}

// sendPremiumOffer answers one offer as the list shows it.
func (s *Server) sendPremiumOffer(c *gin.Context, status int, id int64) {
	rows, err := s.store.ListPremiumOffers(c, db.ListPremiumOffersParams{Lim: 50, Off: 0})
	if err != nil {
		internalServerError(c, err)
		return
	}
	for _, row := range rows {
		if row.ID == id {
			responses, err := s.premiumOfferResponses(c, []db.ListPremiumOffersRow{row})
			if err != nil {
				internalServerError(c, err)
				return
			}
			apiResponse(c, status, responses[0])
			return
		}
	}
	resourceNotFoundError(c, "PremiumOffer")
}

// DELETE /v1/admin/premium/offers/:id: the offer taken back; its members
// keep only the Premium they have otherwise.
func (s *Server) revokePremiumOffer(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		resourceNotFoundError(c, "PremiumOffer")
		return
	}
	if _, err := s.store.GetPremiumOffer(c, id); errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "PremiumOffer")
		return
	} else if err != nil {
		internalServerError(c, err)
		return
	}
	rows, err := s.store.RevokePremiumOffer(c, id)
	if err != nil {
		internalServerError(c, err)
		return
	}
	// its gifts no longer count (premiumBought): nothing else to do
	logFor(c).Info("premium offer taken back", "offer", id, "members", len(rows))
	s.sendPremiumOffer(c, http.StatusOK, id)
}

type revokeMemberPremiumRequest struct {
	// also what the member bought on the website (tests); superadmins only
	IncludePaid bool `json:"include_paid"`
}

type revokeMemberPremiumResponse struct {
	Username     string `json:"username"`
	GiftsRevoked int64  `json:"gifts_revoked"`
	PaidEnded    int64  `json:"paid_ended"`
}

// POST /v1/admin/premium/members/:username/revoke: the member loses all the
// Premium offered to them, and with include_paid (a superadmin) the Premium
// bought on the website too.
func (s *Server) revokeMemberPremium(c *gin.Context) {
	var req revokeMemberPremiumRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		badRequestError(c, err)
		return
	}
	if req.IncludePaid {
		authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
		role, err := s.store.GetUserRole(c, authUser.ID)
		if err != nil || role != roleSuperadmin {
			forbiddenError(c)
			return
		}
	}
	username := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(c.Param("username")), "@"))
	user, err := s.store.GetUserByUsername(c, username)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, NewErrorResponse(http.StatusNotFound, "USERNAME_NOT_FOUND", err))
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	gifts, err := s.store.RevokeUserGifts(c, user.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	var paid int64
	if req.IncludePaid {
		if paid, err = s.store.EndUserPremiumPayments(c, nullID(user.ID)); err != nil {
			internalServerError(c, err)
			return
		}
	}
	logFor(c).Info("premium of a member taken back", "user", user.ID, "gifts", gifts, "paid", paid)
	apiResponse(c, http.StatusOK, revokeMemberPremiumResponse{Username: user.Username, GiftsRevoked: gifts, PaidEnded: paid})
}

// ----- a month offered for a streak -----

// streakGiftDays: the days in a row of reports that offer a month of Premium,
// once, to a member who doesn't have it: they taste it, and the reminder
// before its end (in the app) invites them to go on.
const streakGiftDays = 21

var streakGiftTitles = map[string]string{
	"fr": "%d jours de suite !",
	"en": "%d days in a row!",
	"de": "%d Tage am Stück!",
	"es": "¡%d días seguidos!",
	"ar": "%d يومًا متتالية!",
	"zh": "连续 %d 天！",
	"hi": "लगातार %d दिन!",
}

var streakGiftBodies = map[string]string{
	"fr": "Pour ta fidélité, CMFI Connect Premium t'est offert pendant 1 mois. Continue !",
	"en": "For your faithfulness, CMFI Connect Premium is yours for 1 month. Keep going!",
	"de": "Für deine Treue schenken wir dir CMFI Connect Premium für 1 Monat. Mach weiter so!",
	"es": "Por tu fidelidad, te regalamos CMFI Connect Premium durante 1 mes. ¡Sigue así!",
	"ar": "لأمانتك، نهديك CMFI Connect Premium لمدة شهر. واصل!",
	"zh": "感谢你的忠心，送你 1 个月 CMFI Connect Premium。继续加油！",
	"hi": "तुम्हारी निष्ठा के लिए, CMFI Connect Premium 1 महीने के लिए तुम्हें उपहार में। लगे रहो!",
}

// giveStreakGift offers a month of Premium to [userID] once their reports
// reach [streakGiftDays] days in a row, if they never had this gift, have no
// Premium now and are not on the team. Done in the background after a report
// is saved; it changes nothing for the report.
func (s *Server) giveStreakGift(ctx context.Context, userID int64) {
	given, err := s.store.HasStreakGift(ctx, userID)
	if err != nil || given {
		return
	}
	if role, err := s.store.GetUserRole(ctx, userID); err != nil || role == roleAdmin || role == roleSuperadmin {
		return
	}
	lifetime, until, err := s.premiumBought(ctx, userID)
	if err != nil || lifetime || until != nil && until.After(time.Now()) {
		return
	}
	day := today()
	rows, err := s.store.ListReportDays(ctx, db.ListReportDaysParams{
		UserIds:  []int64{userID},
		FromDate: day.AddDate(0, 0, -(streakGiftDays + 1)),
		ToDate:   day,
	})
	if err != nil || regularityOf(rows, day, 0).Streak < streakGiftDays {
		return
	}
	user, err := s.store.GetUserById(ctx, userID)
	if err != nil {
		return
	}
	audience, _ := json.Marshal(map[string]any{"username": user.Username, "streak": streakGiftDays})
	offer, err := s.store.CreatePremiumOffer(ctx, db.CreatePremiumOfferParams{
		Months:       1,
		Audience:     audience,
		Note:         fmt.Sprintf("%d jours de suite", streakGiftDays),
		Notify:       true,
		MembersCount: 1,
	})
	if err != nil {
		slog.Error("cannot create a streak gift", "user", userID, "error", err)
		return
	}
	if err := s.store.CreatePremiumGifts(ctx, db.CreatePremiumGiftsParams{
		OfferID: offer.ID, UserIds: []int64{userID}, Months: 1,
	}); err != nil {
		slog.Error("cannot create a streak gift", "user", userID, "error", err)
		return
	}
	s.giveGifts(ctx, sql.NullInt64{Int64: offer.ID, Valid: true}, 1)
	slog.Info("premium offered for a streak", "user", userID, "offer", offer.ID)
}
