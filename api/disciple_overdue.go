package api

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// The dashboard of a disciple maker: which disciples haven't filled their
// report for a while, and a reminder sent in one tap. Only the disciples who
// share their reports can be seen as late: for the others nothing is known.

const (
	defaultOverdueDays = 3
	maxOverdueDays     = 60
	// a disciple is reminded once a day at most
	nudgeInterval = 24 * time.Hour
	maxNudgeLinks = 200
)

type overdueDiscipleResponse struct {
	AuthorityID     int64      `json:"authority_id"`
	Member          memberCard `json:"member"`
	IsDiscipleMaker bool       `json:"is_disciple_maker"`
	// the latest day with a report, null when they never filled one
	LastDay *string `json:"last_day"`
	// the days since that day, null when they never filled one
	DaysSince *int `json:"days_since"`
	// when they were last reminded, null if never
	LastNudgeAt *time.Time `json:"last_nudge_at"`
	// whether a reminder can be sent now (not reminded in the last 24 hours)
	CanNudge bool `json:"can_nudge"`

	userID int64
}

type overdueResponse struct {
	// the threshold used: no report for this many days
	Days int `json:"days"`
	// the disciples who share their reports
	Sharing int `json:"sharing"`
	// those who don't: nothing is known about their reports
	NotSharing int `json:"not_sharing"`
	// the late ones, the longest without a report first
	Overdue []overdueDiscipleResponse `json:"overdue"`
}

type overdueQuery struct {
	Days  int    `form:"days" binding:"omitempty,min=1,max=60"`
	Today string `form:"today"`
}

// overdueDisciples reads which disciples of [memberID] have no report for
// [days] days or more (counting from [day]), the longest first.
func (s *Server) overdueDisciples(c *gin.Context, memberID int64, ids []int64, days int, day time.Time) (overdueResponse, error) {
	response := overdueResponse{Days: days, Overdue: []overdueDiscipleResponse{}}
	links, err := s.store.ListAcceptedDiscipleLinks(c, db.ListAcceptedDiscipleLinksParams{
		MemberID: memberID,
		Ids:      ids,
	})
	if err != nil {
		return response, err
	}

	sharing := make([]db.ListAcceptedDiscipleLinksRow, 0, len(links))
	for _, link := range links {
		if link.SharesRegularity {
			sharing = append(sharing, link)
		} else {
			response.NotSharing++
		}
	}
	response.Sharing = len(sharing)
	if len(sharing) == 0 {
		return response, nil
	}

	userIDs := make([]int64, 0, len(sharing))
	linkIDs := make([]int64, 0, len(sharing))
	for _, link := range sharing {
		userIDs = append(userIDs, link.UserID)
		linkIDs = append(linkIDs, link.ID)
	}
	last := map[int64]time.Time{}
	rows, err := s.store.ListLastReportDays(c, userIDs)
	if err != nil {
		return response, err
	}
	for _, row := range rows {
		last[row.UserID] = row.LastDay
	}
	nudges := map[int64]time.Time{}
	nudgeRows, err := s.store.ListLastNudges(c, linkIDs)
	if err != nil {
		return response, err
	}
	for _, row := range nudgeRows {
		nudges[row.AuthorityID] = row.SentAt
	}

	late := make([]db.ListAcceptedDiscipleLinksRow, 0, len(sharing))
	for _, link := range sharing {
		lastDay, filled := last[link.UserID]
		if !filled || daysBetween(lastDay, day) >= days {
			late = append(late, link)
		}
	}
	cards, err := s.store.ListMemberCards(c, userIDsOf(late))
	if err != nil {
		return response, err
	}
	byID := make(map[int64]db.ListMemberCardsRow, len(cards))
	for _, card := range cards {
		byID[card.ID] = card
	}

	now := time.Now()
	for _, link := range late {
		item := overdueDiscipleResponse{
			AuthorityID: link.ID,
			Member:      toMemberCard(byID[link.UserID]),
			CanNudge:    true,
			userID:      link.UserID,
		}
		if lastDay, ok := last[link.UserID]; ok {
			text := lastDay.Format(reportDateLayout)
			since := daysBetween(lastDay, day)
			item.LastDay, item.DaysSince = &text, &since
		}
		if at, ok := nudges[link.ID]; ok {
			item.LastNudgeAt = &at
			item.CanNudge = now.Sub(at) >= nudgeInterval
		}
		response.Overdue = append(response.Overdue, item)
	}
	// the longest without a report first; those who never filled one lead
	sort.SliceStable(response.Overdue, func(i, j int) bool {
		a, b := response.Overdue[i].DaysSince, response.Overdue[j].DaysSince
		switch {
		case a == nil && b == nil:
			return false
		case a == nil:
			return true
		case b == nil:
			return false
		}
		return *a > *b
	})
	return response, nil
}

func userIDsOf(links []db.ListAcceptedDiscipleLinksRow) []int64 {
	ids := make([]int64, 0, len(links))
	for _, link := range links {
		ids = append(ids, link.UserID)
	}
	return ids
}

// GET /v1/disciples/overdue?days=3&today=YYYY-MM-DD: the disciples who share
// their reports and haven't filled one for [days] days or more.
func (s *Server) listOverdueDisciples(c *gin.Context) {
	var query overdueQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		badRequestError(c, err)
		return
	}
	if query.Days == 0 {
		query.Days = defaultOverdueDays
	}
	day, err := disciplesToday(query.Today)
	if err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	response, err := s.overdueDisciples(c, authUser.ID, nil, query.Days, day)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, response)
}

type nudgeRequest struct {
	// the links (authority ids) to remind; empty: every late disciple
	AuthorityIDs []int64 `json:"authority_ids" binding:"max=200"`
	// the threshold for "every late disciple" (3 by default)
	Days  int    `json:"days" binding:"omitempty,min=1,max=60"`
	Today string `json:"today"`
}

type nudgeResponse struct {
	// the reminders sent
	Sent int `json:"sent"`
	// those not sent: reminded less than a day ago
	Skipped int `json:"skipped"`
}

var reportReminderTitles = map[string]string{
	"fr": "Un petit rappel",
	"en": "A little reminder",
	"de": "Eine kleine Erinnerung",
	"es": "Un pequeño recordatorio",
	"ar": "تذكير بسيط",
	"zh": "温馨提醒",
	"hi": "एक छोटा सा अनुस्मारक",
}

var reportReminderBodies = map[string]string{
	"fr": "%s pense à toi : ton compte rendu t'attend",
	"en": "%s is thinking of you: your report is waiting",
	"de": "%s denkt an dich: dein Bericht wartet auf dich",
	"es": "%s piensa en ti: tu informe te espera",
	"ar": "%s يفكر فيك: تقريرك ينتظرك",
	"zh": "%s 在想念你：你的报告在等你",
	"hi": "%s तुम्हें याद कर रहे हैं: तुम्हारी रिपोर्ट इंतज़ार कर रही है",
}

// POST /v1/disciples/nudge: reminds some disciples (or all the late ones) to
// fill their report, by a push. A disciple is reminded once a day at most.
func (s *Server) nudgeDisciples(c *gin.Context) {
	var req nudgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	var targets []overdueDiscipleResponse
	if len(req.AuthorityIDs) == 0 {
		if req.Days == 0 {
			req.Days = defaultOverdueDays
		}
		day, err := disciplesToday(req.Today)
		if err != nil {
			badRequestError(c, err)
			return
		}
		late, err := s.overdueDisciples(c, authUser.ID, nil, req.Days, day)
		if err != nil {
			internalServerError(c, err)
			return
		}
		targets = late.Overdue
	} else {
		// the links named, only those of the signed-in member's disciples
		links, err := s.store.ListAcceptedDiscipleLinks(c, db.ListAcceptedDiscipleLinksParams{
			MemberID: authUser.ID,
			Ids:      req.AuthorityIDs,
		})
		if err != nil {
			internalServerError(c, err)
			return
		}
		if len(links) != len(uniqueIDs(req.AuthorityIDs)) {
			resourceNotFoundError(c, "Disciple")
			return
		}
		ids := make([]int64, 0, len(links))
		byLink := map[int64]db.ListAcceptedDiscipleLinksRow{}
		for _, link := range links {
			ids = append(ids, link.ID)
			byLink[link.ID] = link
		}
		nudges, err := s.store.ListLastNudges(c, ids)
		if err != nil {
			internalServerError(c, err)
			return
		}
		last := map[int64]time.Time{}
		for _, row := range nudges {
			last[row.AuthorityID] = row.SentAt
		}
		now := time.Now()
		for _, id := range ids {
			item := overdueDiscipleResponse{AuthorityID: id, CanNudge: true, userID: byLink[id].UserID}
			if at, ok := last[id]; ok {
				item.CanNudge = now.Sub(at) >= nudgeInterval
			}
			targets = append(targets, item)
		}
	}

	response := nudgeResponse{}
	name := s.firstNameOf(c, authUser.ID)
	for _, target := range targets {
		if !target.CanNudge {
			response.Skipped++
			continue
		}
		if err := s.store.CreateDiscipleNudge(c, target.AuthorityID); err != nil {
			internalServerError(c, fmt.Errorf("record a reminder: %w", err))
			return
		}
		go s.pushToMember(target.userID, reportReminderTitles, reportReminderBodies, name, "report_reminder", target.AuthorityID)
		response.Sent++
	}
	apiResponse(c, http.StatusOK, response)
}

func uniqueIDs(ids []int64) map[int64]bool {
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}
