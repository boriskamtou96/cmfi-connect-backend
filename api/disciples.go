package api

import (
	"math"
	"net/http"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// discipleRegularity sums up the days a disciple filled in, up to today.
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
}

// discipleResponse is a member who chose the signed-in member as their
// disciple maker. Their regularity is null when they don't share it.
type discipleResponse struct {
	AuthorityID      int64               `json:"authority_id"`
	Member           memberCard          `json:"member"`
	SharesRegularity bool                `json:"shares_regularity"`
	Regularity       *discipleRegularity `json:"regularity"`
}

type disciplesQuery struct {
	Today string `form:"today"`
}

// GET /v1/disciples?today=YYYY-MM-DD: the members who chose the signed-in
// member as their disciple maker, with their regularity when they share it.
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
		internalServerError(c)
		return
	}

	ids := make([]int64, 0, len(disciples))
	sharing := make([]int64, 0, len(disciples))
	for _, disciple := range disciples {
		ids = append(ids, disciple.UserID)
		if disciple.SharesRegularity {
			sharing = append(sharing, disciple.UserID)
		}
	}
	cards := map[int64]db.ListMemberCardsRow{}
	if len(ids) > 0 {
		rows, err := s.store.ListMemberCards(c, ids)
		if err != nil {
			internalServerError(c)
			return
		}
		for _, row := range rows {
			cards[row.ID] = row
		}
	}

	// the report days of those who share them, in one query
	days := map[int64][]db.ListReportDaysRow{}
	if len(sharing) > 0 {
		rows, err := s.store.ListReportDays(c, db.ListReportDaysParams{
			UserIds:  sharing,
			FromDate: day.AddDate(0, 0, -(maxReportRangeDays - 1)),
			ToDate:   day,
		})
		if err != nil {
			internalServerError(c)
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
			SharesRegularity: disciple.SharesRegularity,
		}
		if disciple.SharesRegularity {
			regularity := regularityOf(days[disciple.UserID], day)
			response.Regularity = &regularity
		}
		responses = append(responses, response)
	}
	apiResponse(c, http.StatusOK, responses)
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
// [day]: the same streak as the app counts.
func regularityOf(rows []db.ListReportDaysRow, day time.Time) discipleRegularity {
	regularity := discipleRegularity{Days: make([]string, 0)}
	monthStart := day.AddDate(0, 0, -(defaultReportDays - 1)).Format(reportDateLayout)
	filled := make(map[string]bool, len(rows))
	goalSum, goalDays := 0, 0
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
