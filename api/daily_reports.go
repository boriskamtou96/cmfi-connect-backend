package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

const (
	reportDateLayout   = "2006-01-02"
	defaultReportDays  = 30
	maxReportRangeDays = 366
)

type reportDateURI struct {
	Date string `uri:"date" binding:"required"`
}

type dailyReportFormEntry struct {
	ActivityTypeID  int64   `json:"activity_type_id"`
	Code            string  `json:"code"`
	Label           string  `json:"label"`
	TracksQuantity  bool    `json:"tracks_quantity"`
	QuantityUnit    *string `json:"quantity_unit"`
	TracksDuration  bool    `json:"tracks_duration"`
	Quantity        *int32  `json:"quantity"`
	DurationMinutes *int32  `json:"duration_minutes"`
}

// dailyReportFormResponse is the day form: every available item, with the values entered that day.
type dailyReportFormResponse struct {
	Date      string                 `json:"date"`
	Note      *string                `json:"note"`
	Submitted bool                   `json:"submitted"`
	Entries   []dailyReportFormEntry `json:"entries"`
}

func (s *Server) getDailyReport(c *gin.Context) {
	var uri reportDateURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}

	date, err := parseReportDate(uri.Date)
	if err != nil {
		badRequestError(c, err)
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	form, err := s.buildDailyReportForm(c, authUser.ID, date)
	if err != nil {
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusOK, form)
}

type saveDailyReportEntry struct {
	ActivityTypeID  int64  `json:"activity_type_id" binding:"required,min=1"`
	Quantity        *int32 `json:"quantity" binding:"omitempty,min=0,max=100000"`
	DurationMinutes *int32 `json:"duration_minutes" binding:"omitempty,min=0,max=1440"`
}

type saveDailyReportPayload struct {
	Note    *string                `json:"note" binding:"omitempty,max=1000"`
	Entries []saveDailyReportEntry `json:"entries" binding:"max=50,dive"`
}

// saveDailyReport replaces the whole day: items left out of entries are removed from that day.
func (s *Server) saveDailyReport(c *gin.Context) {
	var uri reportDateURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}

	date, err := parseReportDate(uri.Date)
	if err != nil {
		badRequestError(c, err)
		return
	}
	if date.After(today()) {
		badRequestError(c, errors.New("cannot save a report for a future date"))
		return
	}

	var payload saveDailyReportPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		badRequestError(c, err)
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	params := db.SaveDailyReportTxParams{
		UserID:     authUser.ID,
		ReportDate: date,
		Entries:    make([]db.ReportEntryInput, 0, len(payload.Entries)),
	}

	if payload.Note != nil {
		note := strings.TrimSpace(*payload.Note)
		params.Note = sql.NullString{
			String: note,
			Valid:  note != "",
		}
	}

	for _, entry := range payload.Entries {
		input := db.ReportEntryInput{ActivityTypeID: entry.ActivityTypeID}
		if entry.Quantity != nil {
			input.Quantity = sql.NullInt32{
				Int32: *entry.Quantity,
				Valid: true,
			}
		}
		if entry.DurationMinutes != nil {
			input.DurationMinutes = sql.NullInt32{
				Int32: *entry.DurationMinutes,
				Valid: true,
			}
		}
		params.Entries = append(params.Entries, input)
	}

	_, err = s.store.SaveDailyReportTx(c, params)
	if err != nil {
		if errors.Is(err, db.ErrInvalidReportEntry) {
			badRequestError(c, err)
			return
		}
		internalServerError(c)
		return
	}

	// send back the saved day, exactly as GET /reports/:date would
	form, err := s.buildDailyReportForm(c, authUser.ID, date)
	if err != nil {
		internalServerError(c)
		return
	}

	apiResponse(c, http.StatusOK, form)
}

type reportRangeQuery struct {
	From string `form:"from"`
	To   string `form:"to"`
}

type dailyReportHistoryEntry struct {
	ActivityTypeID  int64   `json:"activity_type_id"`
	Code            string  `json:"code"`
	Label           string  `json:"label"`
	QuantityUnit    *string `json:"quantity_unit"`
	Quantity        *int32  `json:"quantity"`
	DurationMinutes *int32  `json:"duration_minutes"`
}

type dailyReportHistoryDay struct {
	Date    string                    `json:"date"`
	Note    *string                   `json:"note"`
	Entries []dailyReportHistoryEntry `json:"entries"`
}

// listDailyReports returns the days that have a report, newest first.
func (s *Server) listDailyReports(c *gin.Context) {
	from, to, err := parseReportRange(c)
	if err != nil {
		badRequestError(c, err)
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	rows, err := s.store.ListDailyReportEntries(c, db.ListDailyReportEntriesParams{
		UserID:   authUser.ID,
		FromDate: from,
		ToDate:   to,
	})
	if err != nil {
		internalServerError(c)
		return
	}

	// rows are sorted by date: a new date starts a new day
	days := make([]dailyReportHistoryDay, 0)
	for _, row := range rows {
		date := row.ReportDate.Format(reportDateLayout)
		if len(days) == 0 || days[len(days)-1].Date != date {
			days = append(days, dailyReportHistoryDay{
				Date:    date,
				Note:    nullStringPtr(row.Note),
				Entries: make([]dailyReportHistoryEntry, 0),
			})
		}
		if !row.ActivityTypeID.Valid {
			continue // a day with a note but no entries
		}

		day := &days[len(days)-1]
		day.Entries = append(day.Entries, dailyReportHistoryEntry{
			ActivityTypeID:  row.ActivityTypeID.Int64,
			Code:            row.Code.String,
			Label:           row.Label.String,
			QuantityUnit:    nullStringPtr(row.QuantityUnit),
			Quantity:        nullInt32Ptr(row.Quantity),
			DurationMinutes: nullInt32Ptr(row.DurationMinutes),
		})
	}

	apiResponse(c, http.StatusOK, days)
}

type reportSummaryItem struct {
	ActivityTypeID int64   `json:"activity_type_id"`
	Code           string  `json:"code"`
	Label          string  `json:"label"`
	QuantityUnit   *string `json:"quantity_unit"`
	TracksQuantity bool    `json:"tracks_quantity"`
	TracksDuration bool    `json:"tracks_duration"`
	DaysCount      int64   `json:"days_count"`
	TotalQuantity  int64   `json:"total_quantity"`
	TotalMinutes   int64   `json:"total_minutes"`
}

type reportSummaryResponse struct {
	From         string              `json:"from"`
	To           string              `json:"to"`
	DaysInRange  int                 `json:"days_in_range"`
	DaysReported int64               `json:"days_reported"`
	Items        []reportSummaryItem `json:"items"`
}

func (s *Server) getReportSummary(c *gin.Context) {
	from, to, err := parseReportRange(c)
	if err != nil {
		badRequestError(c, err)
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	daysReported, err := s.store.CountReportedDays(c, db.CountReportedDaysParams{
		UserID:   authUser.ID,
		FromDate: from,
		ToDate:   to,
	})
	if err != nil {
		internalServerError(c)
		return
	}

	rows, err := s.store.GetReportSummary(c, db.GetReportSummaryParams{
		UserID:   authUser.ID,
		FromDate: from,
		ToDate:   to,
	})
	if err != nil {
		internalServerError(c)
		return
	}

	items := make([]reportSummaryItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, reportSummaryItem{
			ActivityTypeID: row.ActivityTypeID,
			Code:           row.Code,
			Label:          row.Label,
			QuantityUnit:   nullStringPtr(row.QuantityUnit),
			TracksQuantity: row.TracksQuantity,
			TracksDuration: row.TracksDuration,
			DaysCount:      row.DaysCount,
			TotalQuantity:  row.TotalQuantity,
			TotalMinutes:   row.TotalMinutes,
		})
	}

	apiResponse(c, http.StatusOK, reportSummaryResponse{
		From:         from.Format(reportDateLayout),
		To:           to.Format(reportDateLayout),
		DaysInRange:  daysBetween(from, to) + 1,
		DaysReported: daysReported,
		Items:        items,
	})
}

func (s *Server) buildDailyReportForm(c *gin.Context, userID int64, date time.Time) (dailyReportFormResponse, error) {
	form := dailyReportFormResponse{
		Date:    date.Format(reportDateLayout),
		Entries: make([]dailyReportFormEntry, 0),
	}

	report, err := s.store.GetDailyReport(c, db.GetDailyReportParams{
		UserID:     userID,
		ReportDate: date,
	})
	switch {
	case err == nil:
		form.Submitted = true
		form.Note = nullStringPtr(report.Note)
	case !errors.Is(err, sql.ErrNoRows):
		return form, err
	}

	rows, err := s.store.GetDailyReportForm(c, db.GetDailyReportFormParams{
		UserID:     userID,
		ReportDate: date,
	})
	if err != nil {
		return form, err
	}

	for _, row := range rows {
		form.Entries = append(form.Entries, dailyReportFormEntry{
			ActivityTypeID:  row.ActivityTypeID,
			Code:            row.Code,
			Label:           row.Label,
			TracksQuantity:  row.TracksQuantity,
			QuantityUnit:    nullStringPtr(row.QuantityUnit),
			TracksDuration:  row.TracksDuration,
			Quantity:        nullInt32Ptr(row.Quantity),
			DurationMinutes: nullInt32Ptr(row.DurationMinutes),
		})
	}

	return form, nil
}

func parseReportDate(value string) (time.Time, error) {
	date, err := time.Parse(reportDateLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q, expected YYYY-MM-DD", value)
	}
	return date, nil
}

// parseReportRange reads ?from=&to=. Defaults: the last 30 days up to today.
func parseReportRange(c *gin.Context) (time.Time, time.Time, error) {
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
		to = parsed
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
	if daysBetween(from, to)+1 > maxReportRangeDays {
		return time.Time{}, time.Time{}, fmt.Errorf("the range cannot exceed %d days", maxReportRangeDays)
	}

	return from, to, nil
}

// today is the server's current date, at midnight UTC like the dates parsed from requests.
// A user in another time zone can be a day ahead: this check may need the user's time zone later.
func today() time.Time {
	year, month, day := time.Now().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func daysBetween(from, to time.Time) int {
	return int(to.Sub(from).Hours() / 24)
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullInt32Ptr(value sql.NullInt32) *int32 {
	if !value.Valid {
		return nil
	}
	return &value.Int32
}
