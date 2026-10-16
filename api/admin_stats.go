package api

import (
	"net/http"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
)

// The dashboard of the back office: how many members, how many are active,
// the reports and the retreats day by day, the countries. Counts only: never
// who, never what a member wrote.

type statsMembersResponse struct {
	Total        int64 `json:"total"`
	NewThisWeek  int64 `json:"new_this_week"`
	NewThisMonth int64 `json:"new_this_month"`
	// Active: filled a report or made a retreat in the last 7 days.
	Active int64 `json:"active"`
}

type statsDayResponse struct {
	Day string `json:"day"`
	// Reports: the members who filled their report that day.
	Reports  int64 `json:"reports"`
	Retreats int64 `json:"retreats"`
	// Praying: the members who made at least one retreat that day.
	Praying    int64 `json:"praying"`
	NewMembers int64 `json:"new_members"`
}

type statsCountryResponse struct {
	// Country: its ISO code ("CM"), "" when the profile has none.
	Country string `json:"country"`
	Members int64  `json:"members"`
}

type adminStatsResponse struct {
	Members   statsMembersResponse   `json:"members"`
	Days      []statsDayResponse     `json:"days"`
	Countries []statsCountryResponse `json:"countries"`
}

type adminStatsQuery struct {
	// Days: how many days back, today included (30 by default).
	Days int `form:"days" binding:"omitempty,min=7,max=90"`
}

// GET /v1/admin/stats?days=30
func (s *Server) adminStats(c *gin.Context) {
	var query adminStatsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		badRequestError(c, err)
		return
	}
	if query.Days == 0 {
		query.Days = 30
	}

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	// the week starts on Monday
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	firstOfMonth := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	since := today.AddDate(0, 0, -(query.Days - 1))

	members, err := s.store.CountMembers(c, db.CountMembersParams{
		WeekStart:  monday,
		MonthStart: firstOfMonth,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	active, err := s.store.CountActiveMembers(c, today.AddDate(0, 0, -6))
	if err != nil {
		internalServerError(c, err)
		return
	}
	reports, err := s.store.ListReportsPerDay(c, since)
	if err != nil {
		internalServerError(c, err)
		return
	}
	retreats, err := s.store.ListRetreatsPerDay(c, since)
	if err != nil {
		internalServerError(c, err)
		return
	}
	newcomers, err := s.store.ListNewMembersPerDay(c, since)
	if err != nil {
		internalServerError(c, err)
		return
	}
	countries, err := s.store.ListMembersByCountry(c, 12)
	if err != nil {
		internalServerError(c, err)
		return
	}

	// every day of the period, the empty ones too
	days := make([]statsDayResponse, query.Days)
	index := make(map[string]int, query.Days)
	for i := range days {
		day := since.AddDate(0, 0, i).Format(time.DateOnly)
		days[i] = statsDayResponse{Day: day}
		index[day] = i
	}
	for _, row := range reports {
		if i, ok := index[row.Day.Format(time.DateOnly)]; ok {
			days[i].Reports = row.Members
		}
	}
	for _, row := range retreats {
		if i, ok := index[row.Day.Format(time.DateOnly)]; ok {
			days[i].Retreats = row.Retreats
			days[i].Praying = row.Members
		}
	}
	for _, row := range newcomers {
		if i, ok := index[row.Day.Format(time.DateOnly)]; ok {
			days[i].NewMembers = row.Members
		}
	}

	byCountry := make([]statsCountryResponse, 0, len(countries))
	for _, row := range countries {
		byCountry = append(byCountry, statsCountryResponse{Country: row.Country, Members: row.Members})
	}

	apiResponse(c, http.StatusOK, adminStatsResponse{
		Members: statsMembersResponse{
			Total:        members.Total,
			NewThisWeek:  members.NewThisWeek,
			NewThisMonth: members.NewThisMonth,
			Active:       active,
		},
		Days:      days,
		Countries: byCountry,
	})
}
