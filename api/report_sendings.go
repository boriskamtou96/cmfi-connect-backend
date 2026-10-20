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
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/push"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// A member sends a period of their reports in the app to their authorities
// who are members too (accepted, and to whom the reports are shared). Each
// authority gets a push, finds it in « Comptes rendus reçus » (read with the
// usual GET /v1/disciples/:id/reports for the period) and may answer with a
// word of encouragement, which the disciple gets as a push too. The other
// authorities still get the report by WhatsApp or e-mail, from the phone.

// maxSendingDays caps the period sent: a month and a few days.
const maxSendingDays = 35

// maxReplyLength is the longest answer, in characters.
const maxReplyLength = 500

type reportSendingResponse struct {
	ID int64 `json:"id"`
	// the link: the receiver opens the disciple's reports with it
	AuthorityID int64 `json:"authority_id"`
	// the disciple for the receiver, the receiver for the disciple
	Member     memberCard `json:"member"`
	PeriodFrom string     `json:"period_from"`
	PeriodTo   string     `json:"period_to"`
	SentAt     time.Time  `json:"sent_at"`
	ReadAt     *time.Time `json:"read_at"`
	// '' before an answer
	Reply     string     `json:"reply"`
	RepliedAt *time.Time `json:"replied_at"`
}

func timePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time
	return &at
}

func toReportSending(row db.ReportSending, member memberCard) reportSendingResponse {
	return reportSendingResponse{
		ID:          row.ID,
		AuthorityID: row.AuthorityID,
		Member:      member,
		PeriodFrom:  row.PeriodFrom.Format(reportDateLayout),
		PeriodTo:    row.PeriodTo.Format(reportDateLayout),
		SentAt:      row.SentAt,
		ReadAt:      timePointer(row.ReadAt),
		Reply:       row.Reply,
		RepliedAt:   timePointer(row.RepliedAt),
	}
}

// sendingsWithCards answers [rows] with the member card of each one's other
// side ([other] picks it: the disciple or the receiver).
func (s *Server) sendingsWithCards(c *gin.Context, rows []db.ReportSending, other func(db.ReportSending) int64) ([]reportSendingResponse, error) {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, other(row))
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
	responses := make([]reportSendingResponse, 0, len(rows))
	for _, row := range rows {
		responses = append(responses, toReportSending(row, cards[other(row)]))
	}
	return responses, nil
}

type sendReportRequest struct {
	// "2026-10-12" to "2026-10-18"
	From string `json:"from" binding:"required"`
	To   string `json:"to" binding:"required"`
	// some of the authorities only (ids of the links); empty: all who can
	AuthorityIDs []int64 `json:"authority_ids"`
}

// POST /v1/report-sendings: sends the period to the member authorities who
// can receive it (accepted, shared), or to those of [authority_ids] among
// them. The sendings made; none when no authority can receive it.
func (s *Server) sendReport(c *gin.Context) {
	var req sendReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	from, err := parseReportDate(req.From)
	if err != nil {
		badRequestError(c, err)
		return
	}
	to, err := parseReportDate(req.To)
	if err != nil {
		badRequestError(c, err)
		return
	}
	switch {
	case to.Before(from):
		badRequestError(c, errors.New("to: on or after from"))
		return
	case daysBetween(from, to) >= maxSendingDays:
		badRequestError(c, fmt.Errorf("a period of %d days at most", maxSendingDays))
		return
	case daysBetween(today(), to) > 1:
		badRequestError(c, errors.New("to: not in the future"))
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)

	authorities, err := s.store.ListSendableAuthorities(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	wanted := map[int64]bool{}
	for _, id := range req.AuthorityIDs {
		wanted[id] = true
	}
	rows := []db.ReportSending{}
	for _, authority := range authorities {
		if len(wanted) > 0 && !wanted[authority.ID] {
			continue
		}
		row, err := s.store.UpsertReportSending(c, db.UpsertReportSendingParams{
			AuthorityID: authority.ID,
			DiscipleID:  authUser.ID,
			ReceiverID:  authority.LinkedUserID.Int64,
			PeriodFrom:  from,
			PeriodTo:    to,
		})
		if err != nil {
			internalServerError(c, err)
			return
		}
		rows = append(rows, row)
	}
	responses, err := s.sendingsWithCards(c, rows, func(row db.ReportSending) int64 { return row.ReceiverID })
	if err != nil {
		internalServerError(c, err)
		return
	}
	if len(rows) > 0 {
		receivers := make([]int64, 0, len(rows))
		for _, row := range rows {
			receivers = append(receivers, row.ReceiverID)
		}
		sender := s.firstNameOf(c, authUser.ID)
		s.pushReportSent(rows[0], sender, receivers)
	}
	apiResponse(c, http.StatusCreated, responses)
}

// GET /v1/report-sendings/received?page=&size=: what the disciples sent to
// the signed-in member, the newest first.
func (s *Server) listReceivedReports(c *gin.Context) {
	s.listSendings(c, true)
}

// GET /v1/report-sendings/sent?page=&size=: what the signed-in member sent,
// with the answers.
func (s *Server) listSentReports(c *gin.Context) {
	s.listSendings(c, false)
}

func (s *Server) listSendings(c *gin.Context, received bool) {
	var query pageRequest
	if err := c.ShouldBindQuery(&query); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	limit, offset := query.limits()

	var rows []db.ReportSending
	var total int64
	var err error
	if received {
		rows, err = s.store.ListReceivedReportSendings(c, db.ListReceivedReportSendingsParams{
			ReceiverID: authUser.ID, Limit: limit, Offset: offset,
		})
		if err == nil {
			total, err = s.store.CountReceivedReportSendings(c, authUser.ID)
		}
	} else {
		rows, err = s.store.ListSentReportSendings(c, db.ListSentReportSendingsParams{
			DiscipleID: authUser.ID, Limit: limit, Offset: offset,
		})
		if err == nil {
			total, err = s.store.CountSentReportSendings(c, authUser.ID)
		}
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	responses, err := s.sendingsWithCards(c, rows, func(row db.ReportSending) int64 {
		if received {
			return row.DiscipleID
		}
		return row.ReceiverID
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	paginatedApiResponse(c, http.StatusOK, responses, MetaResponse{
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

type reportSendingURI struct {
	ID int64 `uri:"id" binding:"required,min=1"`
}

// POST /v1/report-sendings/:id/read: its receiver opened it.
func (s *Server) readReportSending(c *gin.Context) {
	var uri reportSendingURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if _, err := s.store.MarkReportSendingRead(c, db.MarkReportSendingReadParams{
		ID: uri.ID, ReceiverID: authUser.ID,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Report sending")
			return
		}
		internalServerError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type replyReportRequest struct {
	Text string `json:"text"`
}

// POST /v1/report-sendings/:id/reply: its receiver's word of encouragement
// (it replaces an earlier one); the disciple gets a push.
func (s *Server) replyReportSending(c *gin.Context) {
	var uri reportSendingURI
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req replyReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	text := strings.TrimSpace(req.Text)
	switch {
	case text == "":
		badRequestError(c, errors.New("text is required"))
		return
	case utf8.RuneCountInString(text) > maxReplyLength:
		badRequestError(c, fmt.Errorf("text: %d characters at most", maxReplyLength))
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	row, err := s.store.ReplyReportSending(c, db.ReplyReportSendingParams{
		ID: uri.ID, ReceiverID: authUser.ID, Reply: text,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "Report sending")
			return
		}
		internalServerError(c, err)
		return
	}
	responses, err := s.sendingsWithCards(c, []db.ReportSending{row}, func(row db.ReportSending) int64 { return row.DiscipleID })
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.pushReportReply(row, s.firstNameOf(c, authUser.ID))
	apiResponse(c, http.StatusOK, responses[0])
}

// ----- the pushes -----

var reportSentTitles = map[string]string{
	"fr": "%s t'a envoyé son compte rendu",
	"en": "%s sent you their report",
	"de": "%s hat dir den Bericht geschickt",
	"es": "%s te envió su informe",
	"ar": "%s أرسل إليك تقريره",
	"zh": "%s 给你发送了汇报",
	"hi": "%s ने तुम्हें अपनी रिपोर्ट भेजी",
}

var reportReplyTitles = map[string]string{
	"fr": "%s t'a répondu",
	"en": "%s answered you",
	"de": "%s hat dir geantwortet",
	"es": "%s te respondió",
	"ar": "%s ردّ عليك",
	"zh": "%s 回复了你",
	"hi": "%s ने तुम्हें जवाब दिया",
}

// titleIn is [titles] for a phone in [language], French by default.
func titleIn(titles map[string]string, language, name string) string {
	base, _, _ := strings.Cut(strings.ToLower(language), "-")
	pattern, ok := titles[base]
	if !ok {
		pattern = titles["fr"]
	}
	return strings.Replace(pattern, "%s", name, 1)
}

// pushReportSent tells the receivers: « Grâce t'a envoyé son compte rendu »,
// the period as body ("12/10 → 18/10"); a touch opens what was received.
func (s *Server) pushReportSent(row db.ReportSending, sender string, receivers []int64) {
	body := row.PeriodFrom.Format("02/01") + " → " + row.PeriodTo.Format("02/01")
	s.pushTo(receivers, reportSentTitles, sender, body, map[string]string{
		"type": "report_sending",
		"id":   strconv.FormatInt(row.ID, 10),
	})
}

// pushReportReply tells the disciple: « Paul t'a répondu », the answer as
// body.
func (s *Server) pushReportReply(row db.ReportSending, receiver string) {
	body := row.Reply
	if utf8.RuneCountInString(body) > maxPushBody {
		body = string([]rune(body)[:maxPushBody-1]) + "…"
	}
	s.pushTo([]int64{row.DiscipleID}, reportReplyTitles, receiver, body, map[string]string{
		"type": "report_reply",
		"id":   strconv.FormatInt(row.ID, 10),
	})
}

// pushTo sends to the phones of [users], each in its language. A failure is
// logged: everything is in the app anyway.
func (s *Server) pushTo(users []int64, titles map[string]string, name, body string, data map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()

	devices, err := s.store.ListDevicesOfUsers(ctx, users)
	if err != nil {
		slog.Error("cannot read the phones for a report push", "type", data["type"], "error", err)
		return
	}
	byLanguage := map[string][]string{}
	for _, device := range devices {
		byLanguage[device.Language] = append(byLanguage[device.Language], device.Token)
	}
	dead := []string{}
	for language, tokens := range byLanguage {
		result, err := s.push.Send(ctx, tokens, push.Message{
			Title: titleIn(titles, language, name),
			Body:  body,
			Data:  data,
		})
		if err != nil {
			slog.Error("report push failed", "type", data["type"], "language", language, "error", err)
		}
		dead = append(dead, result.Dead...)
	}
	if len(dead) > 0 {
		if err := s.store.DeleteDevicesByTokens(ctx, dead); err != nil {
			slog.Error("cannot forget the dead phones", "count", len(dead), "error", err)
		}
	}
}
