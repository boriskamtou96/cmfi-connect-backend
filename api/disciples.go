package api

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// A member who chooses another one as disciple maker or as any spiritual
// authority shares their reports with them: the items filled in with their
// values and the percentages, never the note. They can turn it off for each
// authority (authorities.shares_regularity).

// maxDiscipleReportDays caps the range of GET /v1/disciples/:id/reports.
const maxDiscipleReportDays = 92

// discipleRegularity sums up the days a member filled in, up to today.
type discipleRegularity struct {
	// days in a row with a report; a today not filled in yet doesn't break it
	Streak int `json:"streak"`
	// the days with a report among the 30 ending today, newest first
	Days         []string `json:"days"`
	FilledLast30 int      `json:"filled_last_30"`
	// the latest day with a report within a year, or null
	LastDay *string `json:"last_day"`
	// the average share of goals reached over the 30 days, or null without goals
	GoalPercent *int `json:"goal_percent"`
	// the average share of the items filled in over the 30 days' report days,
	// or null without any
	ItemsPercent *int `json:"items_percent"`
}

// discipleResponse is a member who chose the signed-in member as one of their
// authorities, their disciple maker or not. Their regularity is null when they
// don't share their reports.
type discipleResponse struct {
	AuthorityID      int64               `json:"authority_id"`
	Member           memberCard          `json:"member"`
	IsDiscipleMaker  bool                `json:"is_disciple_maker"`
	SharesRegularity bool                `json:"shares_regularity"`
	Regularity       *discipleRegularity `json:"regularity"`
}

type disciplesQuery struct {
	pageRequest
	Today string `form:"today"`
	// a first or last name, both, or a pseudo (with or without its "@")
	Search string `form:"search"`
}

// GET /v1/disciples?today=YYYY-MM-DD&search=&page=&size=: the members who
// chose the signed-in member as one of their authorities, those who chose them
// as disciple maker first, with their regularity when they share their
// reports, a page at a time.
func (s *Server) listDisciples(c *gin.Context) {
	var query disciplesQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		badRequestError(c, err)
		return
	}
	day, err := disciplesToday(query.Today)
	if err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	disciples, err := s.store.ListDisciples(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}

	// a search needs the names of everyone; without one, those of the page do
	search := strings.TrimPrefix(fold(query.Search), "@")
	var cards map[int64]db.ListMemberCardsRow
	if search != "" {
		if cards, err = s.discipleCards(c, disciples); err != nil {
			internalServerError(c, err)
			return
		}
		kept := []db.ListDisciplesRow{}
		for _, disciple := range disciples {
			if answersSearch(cards[disciple.UserID], search) {
				kept = append(kept, disciple)
			}
		}
		disciples = kept
	}

	limit, offset := query.limits()
	total := len(disciples)
	disciples = disciples[min(int(offset), total):min(int(offset+limit), total)]
	if search == "" {
		if cards, err = s.discipleCards(c, disciples); err != nil {
			internalServerError(c, err)
			return
		}
	}

	sharing := make([]int64, 0, len(disciples))
	for _, disciple := range disciples {
		if disciple.SharesRegularity {
			sharing = append(sharing, disciple.UserID)
		}
	}

	// the report days of those of the page who share them, in one query
	days := map[int64][]db.ListReportDaysRow{}
	if len(sharing) > 0 {
		rows, err := s.store.ListReportDays(c, db.ListReportDaysParams{
			UserIds:  sharing,
			FromDate: day.AddDate(0, 0, -(maxReportRangeDays - 1)),
			ToDate:   day,
		})
		if err != nil {
			internalServerError(c, err)
			return
		}
		for _, row := range rows {
			days[row.UserID] = append(days[row.UserID], row)
		}
	}

	responses := make([]discipleResponse, 0, len(disciples))
	for _, disciple := range disciples {
		response := discipleResponse{
			AuthorityID:      disciple.ID,
			Member:           toMemberCard(cards[disciple.UserID]),
			IsDiscipleMaker:  disciple.IsDiscipleMaker,
			SharesRegularity: disciple.SharesRegularity,
		}
		if disciple.SharesRegularity {
			regularity := regularityOf(days[disciple.UserID], day, int(disciple.TotalItems))
			response.Regularity = &regularity
		}
		responses = append(responses, response)
	}
	paginatedApiResponse(c, http.StatusOK, responses, MetaResponse{
		Total:  int64(total),
		Limit:  limit,
		Offset: offset,
	})
}

// discipleCards are the member cards of [disciples], by user id.
func (s *Server) discipleCards(c *gin.Context, disciples []db.ListDisciplesRow) (map[int64]db.ListMemberCardsRow, error) {
	cards := map[int64]db.ListMemberCardsRow{}
	if len(disciples) == 0 {
		return cards, nil
	}
	ids := make([]int64, 0, len(disciples))
	for _, disciple := range disciples {
		ids = append(ids, disciple.UserID)
	}
	rows, err := s.store.ListMemberCards(c, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		cards[row.ID] = row
	}
	return cards, nil
}

// answersSearch tells whether a member answers a search already folded (case
// and accents left aside, no "@"): "paul", "ndi", "paul ndi" and "ndi paul"
// find Paul Ndi, so does a part of their pseudo.
func answersSearch(card db.ListMemberCardsRow, search string) bool {
	for _, value := range []string{
		card.FirstName + " " + card.LastName,
		card.LastName + " " + card.FirstName,
		card.Username,
	} {
		if strings.Contains(fold(value), search) {
			return true
		}
	}
	return false
}

// disciplesToday is the phone's date when it is within a day of the server's
// (a member may live in another time zone), else the server's.
func disciplesToday(value string) (time.Time, error) {
	serverToday := today()
	if value == "" {
		return serverToday, nil
	}
	day, err := parseReportDate(value)
	if err != nil {
		return time.Time{}, err
	}
	if gap := daysBetween(serverToday, day); gap > 1 || gap < -1 {
		return serverToday, nil
	}
	return day, nil
}

// regularityOf sums up the report days of one member, newest first, up to
// [day]: the same streak as the app counts. [totalItems] is the number of
// items they can fill in.
func regularityOf(rows []db.ListReportDaysRow, day time.Time, totalItems int) discipleRegularity {
	regularity := discipleRegularity{Days: make([]string, 0)}
	monthStart := day.AddDate(0, 0, -(defaultReportDays - 1)).Format(reportDateLayout)
	filled := make(map[string]bool, len(rows))
	goalSum, goalDays, itemsSum := 0, 0, 0
	for _, row := range rows {
		date := row.ReportDate.Format(reportDateLayout)
		filled[date] = true
		if regularity.LastDay == nil {
			regularity.LastDay = &date
		}
		if date < monthStart {
			continue
		}
		regularity.Days = append(regularity.Days, date)
		itemsSum += itemsPercent(int(row.Filled), totalItems)
		if row.GoalPercent.Valid {
			goalSum += int(row.GoalPercent.Int16)
			goalDays++
		}
	}
	regularity.FilledLast30 = len(regularity.Days)
	if goalDays > 0 {
		average := int(math.Round(float64(goalSum) / float64(goalDays)))
		regularity.GoalPercent = &average
	}
	if regularity.FilledLast30 > 0 {
		average := int(math.Round(float64(itemsSum) / float64(regularity.FilledLast30)))
		regularity.ItemsPercent = &average
	}

	// a today not filled in yet doesn't break the series: it counts from yesterday
	if !filled[day.Format(reportDateLayout)] {
		day = day.AddDate(0, 0, -1)
	}
	for regularity.Streak < maxReportRangeDays && filled[day.Format(reportDateLayout)] {
		regularity.Streak++
		day = day.AddDate(0, 0, -1)
	}
	return regularity
}

// itemsPercent is the share of the items filled in on a day, rounded, 0
// without items. Items archived since still count as filled: it stops at 100.
func itemsPercent(filled, total int) int {
	if total <= 0 {
		return 0
	}
	return min(100, int(math.Round(float64(filled)*100/float64(total))))
}

type discipleReportsURI struct {
	ID int64 `uri:"id" binding:"required,min=1"`
}

// discipleReportDay is one day a member filled in, as their authority sees
// it: no note.
type discipleReportDay struct {
	Date string `json:"date"`
	// the share of the day's goals reached, or null without goals
	GoalPercent *int `json:"goal_percent"`
	// the items filled in that day, out of total (the items they can fill in)
	Filled       int                       `json:"filled"`
	Total        int                       `json:"total"`
	ItemsPercent int                       `json:"items_percent"`
	Entries      []dailyReportHistoryEntry `json:"entries"`
}

type discipleReportsResponse struct {
	AuthorityID     int64      `json:"authority_id"`
	Member          memberCard `json:"member"`
	IsDiscipleMaker bool       `json:"is_disciple_maker"`
	// the items the member can fill in today: the standard ones and their own
	TotalItems int `json:"total_items"`
	// the days with a report, newest first
	Days []discipleReportDay `json:"days"`
}

// GET /v1/disciples/:id/reports?from=&to=: the reports of a member who chose
// the signed-in member as one of their authorities ([id] is that authority),
// without their notes. 403 REPORTS_NOT_SHARED when they turned sharing off.
func (s *Server) listDiscipleReports(c *gin.Context) {
	var uri discipleReportsURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	// only the member asked, once they accepted; anyone else gets a 404
	authority, err := s.store.GetAuthorityById(c, uri.ID)
	if err != nil || !authority.LinkedUserID.Valid ||
		authority.LinkedUserID.Int64 != authUser.ID || authority.Status != authorityAccepted {
		if err == nil || errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Disciple")
		} else {
			internalServerError(c, err)
		}
		return
	}
	if !authority.SharesRegularity {
		c.JSON(http.StatusForbidden, NewErrorResponse(http.StatusForbidden, "REPORTS_NOT_SHARED",
			fmt.Errorf("this member doesn't share their reports with you")))
		return
	}

	from, to, err := discipleReportRange(c)
	if err != nil {
		badRequestError(c, err)
		return
	}

	cards, err := s.store.ListMemberCards(c, []int64{authority.UserID})
	if err != nil {
		internalServerError(c, err)
		return
	}
	if len(cards) == 0 {
		resourceNotFoundError(c, "Disciple")
		return
	}

	// the items their own day form lists
	items, err := s.store.GetUserActivityTypes(c, authority.UserID)
	if err != nil {
		internalServerError(c, err)
		return
	}

	rows, err := s.store.ListDailyReportEntries(c, db.ListDailyReportEntriesParams{
		UserID:   authority.UserID,
		FromDate: from,
		ToDate:   to,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}

	total := len(items)
	// rows are sorted by date: a new date starts a new day
	days := make([]discipleReportDay, 0)
	for _, row := range rows {
		date := row.ReportDate.Format(reportDateLayout)
		if len(days) == 0 || days[len(days)-1].Date != date {
			report := discipleReportDay{
				Date:    date,
				Total:   total,
				Entries: make([]dailyReportHistoryEntry, 0),
			}
			if row.GoalPercent.Valid {
				goal := int(row.GoalPercent.Int16)
				report.GoalPercent = &goal
			}
			days = append(days, report)
		}
		if row.ActivityTypeID.Valid {
			day := &days[len(days)-1]
			day.Entries = append(day.Entries, toHistoryEntry(row))
		}
	}
	for i := range days {
		days[i].Filled = len(days[i].Entries)
		days[i].ItemsPercent = itemsPercent(days[i].Filled, total)
	}

	apiResponse(c, http.StatusOK, discipleReportsResponse{
		AuthorityID:     authority.ID,
		Member:          toMemberCard(cards[0]),
		IsDiscipleMaker: authority.IsDiscipleMaker,
		TotalItems:      total,
		Days:            days,
	})
}

// discipleReportRange reads ?from=&to=: the 30 days up to the server's today
// by default; a to in the future stops at today, 92 days at most.
func discipleReportRange(c *gin.Context) (time.Time, time.Time, error) {
	var query reportRangeQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		return time.Time{}, time.Time{}, err
	}

	to := today()
	if query.To != "" {
		parsed, err := parseReportDate(query.To)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		if parsed.Before(to) {
			to = parsed
		}
	}

	from := to.AddDate(0, 0, -(defaultReportDays - 1))
	if query.From != "" {
		parsed, err := parseReportDate(query.From)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		from = parsed
	}

	if from.After(to) {
		return time.Time{}, time.Time{}, errors.New("from must be before to")
	}
	if daysBetween(from, to)+1 > maxDiscipleReportDays {
		return time.Time{}, time.Time{}, fmt.Errorf("the range cannot exceed %d days", maxDiscipleReportDays)
	}
	return from, to, nil
}
