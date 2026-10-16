package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/usernames"
	"github.com/gin-gonic/gin"
)

// ----- blocking and reporting a member (an invitation to an importunity
// topic): what the stores ask of an app where members reach each other -----

// memberByUsername is the member of the path, or an answer already given
// (404, or 400 for oneself).
func (s *Server) memberByUsername(c *gin.Context, self int64) (db.User, bool) {
	var uri usernameRequest
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return db.User{}, false
	}
	user, err := s.store.GetUserByUsername(c, usernames.Normalize(uri.Username))
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "member")
		return db.User{}, false
	}
	if err != nil {
		internalServerError(c, err)
		return db.User{}, false
	}
	if user.ID == self {
		badRequestError(c, errors.New("a member can't block or report themselves"))
		return db.User{}, false
	}
	return user, true
}

// POST /v1/members/:username/block: no more invitations from that member;
// the ones not answered yet go. They are not told.
func (s *Server) blockMember(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	blocked, ok := s.memberByUsername(c, authUser.ID)
	if !ok {
		return
	}
	if err := s.store.BlockMember(c, db.BlockMemberParams{BlockerID: authUser.ID, BlockedID: blocked.ID}); err != nil {
		internalServerError(c, err)
		return
	}
	if err := s.store.DropInvitationsFrom(c, db.DropInvitationsFromParams{
		BlockerID: authUser.ID,
		BlockedID: sql.NullInt64{Int64: blocked.ID, Valid: true},
	}); err != nil {
		internalServerError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DELETE /v1/members/:username/block
func (s *Server) unblockMember(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	blocked, ok := s.memberByUsername(c, authUser.ID)
	if !ok {
		return
	}
	if err := s.store.UnblockMember(c, db.UnblockMemberParams{BlockerID: authUser.ID, BlockedID: blocked.ID}); err != nil {
		internalServerError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// GET /v1/members/blocked: the members one blocked, the latest first.
func (s *Server) listBlockedMembers(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	ids, err := s.store.ListBlockedIDs(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	cards, err := s.store.ListMemberCards(c, ids)
	if err != nil {
		internalServerError(c, err)
		return
	}
	byID := make(map[int64]db.ListMemberCardsRow, len(cards))
	for _, card := range cards {
		byID[card.ID] = card
	}
	blocked := make([]memberCard, 0, len(ids))
	for _, id := range ids {
		if card, ok := byID[id]; ok {
			blocked = append(blocked, toMemberCard(card))
		}
	}
	apiResponse(c, http.StatusOK, blocked)
}

// withoutBlockers leaves out of [invited] those who blocked [inviter].
func (s *Server) withoutBlockers(c *gin.Context, inviter int64, invited []int64) ([]int64, error) {
	if len(invited) == 0 {
		return invited, nil
	}
	blockers, err := s.store.ListBlockersAmong(c, db.ListBlockersAmongParams{BlockedID: inviter, UserIds: invited})
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(invited, func(id int64) bool { return slices.Contains(blockers, id) }), nil
}

var reportReasons = []string{"spam", "offensive", "other"}

const maxReportComment = 500

type reportRequest struct {
	Username      string `json:"username" binding:"required"`
	ImportunityID *int64 `json:"importunity_id"`
	Reason        string `json:"reason" binding:"required"`
	Comment       string `json:"comment"`
}

// POST /v1/reports: a member reports another one to the admins, about an
// importunity topic or not.
func (s *Server) createReport(c *gin.Context) {
	var req reportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if !slices.Contains(reportReasons, req.Reason) {
		badRequestError(c, fmt.Errorf("reason: one of %s", strings.Join(reportReasons, ", ")))
		return
	}
	comment := strings.TrimSpace(req.Comment)
	if utf8.RuneCountInString(comment) > maxReportComment {
		badRequestError(c, fmt.Errorf("comment: %d characters at most", maxReportComment))
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	reported, err := s.store.GetUserByUsername(c, usernames.Normalize(req.Username))
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "member")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	if reported.ID == authUser.ID {
		badRequestError(c, errors.New("a member can't report themselves"))
		return
	}
	params := db.CreateReportParams{
		ReporterID: authUser.ID,
		ReportedID: reported.ID,
		Reason:     req.Reason,
		Comment:    comment,
	}
	if req.ImportunityID != nil {
		// the title as it is now: the report keeps it if the topic goes
		if topic, err := s.store.GetImportunity(c, *req.ImportunityID); err == nil {
			params.ImportunityID = sql.NullInt64{Int64: topic.ID, Valid: true}
			params.ImportunityTitle = topic.Title
		}
	}
	id, err := s.store.CreateReport(c, params)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusCreated, gin.H{"id": id})
}

// ----- the reports in the back office -----

type reportedMember struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Role      string `json:"role,omitempty"`
}

type reportTopic struct {
	ID    *int64 `json:"id"`
	Title string `json:"title"`
}

type reportResponse struct {
	ID                       int64          `json:"id"`
	Reason                   string         `json:"reason"`
	Comment                  string         `json:"comment"`
	Status                   string         `json:"status"`
	CreatedAt                time.Time      `json:"created_at"`
	ResolvedAt               *time.Time     `json:"resolved_at"`
	Reporter                 reportedMember `json:"reporter"`
	Reported                 reportedMember `json:"reported"`
	Importunity              *reportTopic   `json:"importunity"`
	OpenReportsAboutReported int32          `json:"open_reports_about_reported"`
}

// reportResponses gives the reports with the names of both members.
func (s *Server) reportResponses(c *gin.Context, rows []db.ListReportsRow) ([]reportResponse, error) {
	ids := []int64{}
	for _, row := range rows {
		ids = append(ids, row.ReporterID, row.ReportedID)
	}
	cards, err := s.store.ListMemberCards(c, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]db.ListMemberCardsRow, len(cards))
	for _, card := range cards {
		byID[card.ID] = card
	}
	member := func(id int64) reportedMember {
		card := byID[id]
		return reportedMember{ID: id, Username: card.Username, FirstName: card.FirstName, LastName: card.LastName}
	}
	reports := make([]reportResponse, 0, len(rows))
	for _, row := range rows {
		report := reportResponse{
			ID:                       row.ID,
			Reason:                   row.Reason,
			Comment:                  row.Comment,
			Status:                   row.Status,
			CreatedAt:                row.CreatedAt,
			Reporter:                 member(row.ReporterID),
			Reported:                 member(row.ReportedID),
			OpenReportsAboutReported: row.OpenAboutReported,
		}
		report.Reported.Role = row.ReportedRole
		if row.ResolvedAt.Valid {
			report.ResolvedAt = &row.ResolvedAt.Time
		}
		if row.ImportunityTitle != "" || row.ImportunityID.Valid {
			topic := &reportTopic{Title: row.ImportunityTitle}
			if row.ImportunityID.Valid {
				topic.ID = &row.ImportunityID.Int64
			}
			report.Importunity = topic
		}
		reports = append(reports, report)
	}
	return reports, nil
}

type reportsRequest struct {
	pageRequest
	// open, resolved, or empty for all
	Status string `form:"status"`
}

// GET /v1/admin/reports?status=open|resolved&page=&size=: the newest first.
func (s *Server) adminListReports(c *gin.Context) {
	var req reportsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if req.Status != "" && req.Status != "open" && req.Status != "resolved" {
		badRequestError(c, errors.New("status: open, resolved or empty"))
		return
	}
	limit, offset := req.limits()
	rows, err := s.store.ListReports(c, db.ListReportsParams{Status: req.Status, Lim: limit, Off: offset})
	if err != nil {
		internalServerError(c, err)
		return
	}
	total, err := s.store.CountReports(c, req.Status)
	if err != nil {
		internalServerError(c, err)
		return
	}
	reports, err := s.reportResponses(c, rows)
	if err != nil {
		internalServerError(c, err)
		return
	}
	paginatedApiResponse(c, http.StatusOK, reports, MetaResponse{Total: total, Limit: limit, Offset: offset})
}

// PUT /v1/admin/reports/:id/resolve: handled (again: nothing changes).
func (s *Server) resolveReport(c *gin.Context) {
	var uri getUserRequest
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	_, err := s.store.ResolveReport(c, db.ResolveReportParams{
		ID:         uri.ID,
		ResolvedBy: sql.NullInt64{Int64: authUser.ID, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "report")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	row, err := s.store.GetReport(c, uri.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	reports, err := s.reportResponses(c, []db.ListReportsRow{db.ListReportsRow(row)})
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, reports[0])
}
