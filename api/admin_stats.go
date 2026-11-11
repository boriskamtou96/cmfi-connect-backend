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
	// ActiveMonth: the same, over the last 30 days.
	ActiveMonth int64 `json:"active_month"`
	// Installs: the phones registered for pushes (an install that signed in).
	Installs int64 `json:"installs"`
}

type statsWeekResponse struct {
	// Week: its Monday.
	Week string `json:"week"`
	// ActiveMembers: filled a report or made a retreat that week.
	ActiveMembers int64 `json:"active_members"`
	Reports       int64 `json:"reports"`
	NewMembers    int64 `json:"new_members"`
}

type statsFeatureResponse struct {
	// Feature: reports, retreats, reading, souls, prayer_lists, importunities,
	// events, authorities, report_sendings.
	Feature string `json:"feature"`
	// Members who used it in the last 30 days.
	Members int64 `json:"members"`
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
	// Weeks: the active members and the reports week by week, the oldest first.
	Weeks []statsWeekResponse `json:"weeks"`
	// Features: who used what in the last 30 days, the most used first.
	Features []statsFeatureResponse `json:"features"`
}

type adminStatsQuery struct {
	// Days: how many days back, today included (30 by default).
	Days int `form:"days" binding:"omitempty,min=7,max=90"`
	// Weeks: how many weeks back, this one included (12 by default).
	Weeks int `form:"weeks" binding:"omitempty,min=4,max=52"`
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
	if query.Weeks == 0 {
		query.Weeks = 12
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
	activeMonth, err := s.store.CountActiveMembers(c, today.AddDate(0, 0, -29))
	if err != nil {
		internalServerError(c, err)
		return
	}
	installs, err := s.store.CountInstalls(c)
	if err != nil {
		internalServerError(c, err)
		return
	}
	firstWeek := monday.AddDate(0, 0, -7*(query.Weeks-1))
	activeWeeks, err := s.store.ListActiveMembersPerWeek(c, firstWeek)
	if err != nil {
		internalServerError(c, err)
		return
	}
	newWeeks, err := s.store.ListNewMembersPerWeek(c, firstWeek)
	if err != nil {
		internalServerError(c, err)
		return
	}
	features, err := s.store.ListFeatureUsage(c, today.AddDate(0, 0, -29))
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

	// every week of the period, the empty ones too
	weeks := make([]statsWeekResponse, query.Weeks)
	weekIndex := make(map[string]int, query.Weeks)
	for i := range weeks {
		week := firstWeek.AddDate(0, 0, 7*i).Format(time.DateOnly)
		weeks[i] = statsWeekResponse{Week: week}
		weekIndex[week] = i
	}
	for _, row := range activeWeeks {
		if i, ok := weekIndex[row.Week.Format(time.DateOnly)]; ok {
			weeks[i].ActiveMembers = row.Members
			weeks[i].Reports = row.Reports
		}
	}
	for _, row := range newWeeks {
		if i, ok := weekIndex[row.Week.Format(time.DateOnly)]; ok {
			weeks[i].NewMembers = row.Members
		}
	}
	used := make([]statsFeatureResponse, 0, len(features))
	for _, row := range features {
		used = append(used, statsFeatureResponse{Feature: row.Feature, Members: row.Members})
	}

	apiResponse(c, http.StatusOK, adminStatsResponse{
		Members: statsMembersResponse{
			Total:        members.Total,
			NewThisWeek:  members.NewThisWeek,
			NewThisMonth: members.NewThisMonth,
			Active:       active,
			ActiveMonth:  activeMonth,
			Installs:     installs,
		},
		Weeks:     weeks,
		Features:  used,
		Days:      days,
		Countries: byCountry,
	})
}
