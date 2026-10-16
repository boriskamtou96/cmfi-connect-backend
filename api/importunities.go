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
	"github.com/boriskamtou96/cmfi-connect-backend/internal/usernames"
	"github.com/gin-gonic/gin"
)

// An importunity topic ("Lord, save Paul"): its participants count their
// proclamations together. Each one sees the total and their own count, never
// the count of the others.

const (
	// one sending of the counter: the app groups the taps
	maxProclamationsPerSend = 10_000
	maxInvitationsPerSend   = 50
)

type importunityResponse struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Goal        *int64     `json:"goal"`
	Total       int64      `json:"total"`
	ReachedAt   *time.Time `json:"reached_at"`
	// open or closed
	Status  string     `json:"status"`
	Owner   memberCard `json:"owner"`
	IsOwner bool       `json:"is_owner"`
	// invited or joined
	MyStatus  string      `json:"my_status"`
	MyCount   int64       `json:"my_count"`
	InvitedBy *memberCard `json:"invited_by"`
	// how many joined, and how many invitations wait for an answer
	Participants int32     `json:"participants"`
	Invited      int32     `json:"invited"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// importunityDetailResponse adds who prays (their names, not their counts).
type importunityDetailResponse struct {
	importunityResponse
	Members []memberCard `json:"members"`
}

// myImportunity is a topic seen by one of its members.
type myImportunity struct {
	importunity db.Importunity
	member      db.ImportunityMember
}

func (s *Server) importunityResponses(c *gin.Context, items []myImportunity, userID int64) ([]importunityResponse, error) {
	responses := make([]importunityResponse, 0, len(items))
	if len(items) == 0 {
		return responses, nil
	}
	ids := make([]int64, 0, len(items))
	people := []int64{}
	for _, item := range items {
		ids = append(ids, item.importunity.ID)
		people = append(people, item.importunity.OwnerID)
		if item.member.InvitedBy.Valid {
			people = append(people, item.member.InvitedBy.Int64)
		}
	}
	counts, err := s.store.CountImportunityMembers(c, ids)
	if err != nil {
		return nil, err
	}
	countOf := map[int64]db.CountImportunityMembersRow{}
	for _, count := range counts {
		countOf[count.ImportunityID] = count
	}
	cards, err := s.memberCards(c, people)
	if err != nil {
		return nil, err
	}

	for _, item := range items {
		importunity, member := item.importunity, item.member
		response := importunityResponse{
			ID:           importunity.ID,
			Title:        importunity.Title,
			Description:  importunity.Description,
			Total:        importunity.Total,
			ReachedAt:    timePtr(importunity.ReachedAt),
			Status:       importunity.Status,
			Owner:        cards[importunity.OwnerID],
			IsOwner:      importunity.OwnerID == userID,
			MyStatus:     member.Status,
			MyCount:      member.Count,
			Participants: countOf[importunity.ID].Joined,
			Invited:      countOf[importunity.ID].Invited,
			CreatedAt:    importunity.CreatedAt,
			UpdatedAt:    importunity.UpdatedAt,
		}
		if importunity.Goal.Valid {
			response.Goal = &importunity.Goal.Int64
		}
		if member.InvitedBy.Valid {
			if card, ok := cards[member.InvitedBy.Int64]; ok {
				response.InvitedBy = &card
			}
		}
		responses = append(responses, response)
	}
	return responses, nil
}

// memberCards: the names of [ids], by id.
func (s *Server) memberCards(c *gin.Context, ids []int64) (map[int64]memberCard, error) {
	cards := map[int64]memberCard{}
	if len(ids) == 0 {
		return cards, nil
	}
	rows, err := s.store.ListMemberCards(c, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		cards[row.ID] = toMemberCard(row)
	}
	return cards, nil
}

func (s *Server) sendImportunity(c *gin.Context, status int, importunity db.Importunity, member db.ImportunityMember) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	responses, err := s.importunityResponses(c, []myImportunity{{importunity, member}}, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	participants, err := s.store.ListImportunityParticipants(c, importunity.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	cards, err := s.memberCards(c, participants)
	if err != nil {
		internalServerError(c, err)
		return
	}
	detail := importunityDetailResponse{importunityResponse: responses[0], Members: []memberCard{}}
	for _, id := range participants {
		if card, ok := cards[id]; ok {
			detail.Members = append(detail.Members, card)
		}
	}
	apiResponse(c, status, detail)
}

// GET /v1/prayer/importunities: the topics the member prays for or is
// invited to (invitations first).
func (s *Server) listImportunities(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	rows, err := s.store.ListMyImportunities(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	items := make([]myImportunity, 0, len(rows))
	for _, row := range rows {
		items = append(items, myImportunity{
			importunity: db.Importunity{
				ID: row.ID, OwnerID: row.OwnerID, Title: row.Title, Description: row.Description, Goal: row.Goal,
				Total: row.Total, ReachedAt: row.ReachedAt, Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			},
			member: db.ImportunityMember{
				ImportunityID: row.ID, UserID: authUser.ID, Status: row.MyStatus, Count: row.MyCount, InvitedBy: row.MyInviter,
			},
		})
	}
	responses, err := s.importunityResponses(c, items, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, responses)
}

type importunityRequest struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	// null or 0: no goal
	Goal *int64 `json:"goal"`
	// open or closed (changing a topic only)
	Status string `json:"status"`
}

func (r importunityRequest) check() (title, description string, goal sql.NullInt64, err error) {
	title = strings.TrimSpace(r.Title)
	description = strings.TrimSpace(r.Description)
	if title == "" {
		return "", "", goal, fmt.Errorf("title is required")
	}
	if len([]rune(title)) > 150 || len([]rune(description)) > 1000 {
		return "", "", goal, fmt.Errorf("title: 150 characters at most, description: 1000")
	}
	if r.Goal != nil && *r.Goal != 0 {
		if *r.Goal < 0 || *r.Goal > 1_000_000_000 {
			return "", "", goal, fmt.Errorf("goal: 1 to 1 000 000 000")
		}
		goal = sql.NullInt64{Int64: *r.Goal, Valid: true}
	}
	return title, description, goal, nil
}

// POST /v1/prayer/importunities: its author is its first participant.
func (s *Server) createImportunity(c *gin.Context) {
	var req importunityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	title, description, goal, err := req.check()
	if err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	importunity, err := s.store.CreateImportunityTx(c, db.CreateImportunityParams{
		OwnerID: authUser.ID, Title: title, Description: description, Goal: goal,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	member, err := s.store.GetImportunityMember(c, db.GetImportunityMemberParams{ImportunityID: importunity.ID, UserID: authUser.ID})
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.sendImportunity(c, http.StatusCreated, importunity, member)
}

// loadImportunity finds the topic of the URL and the signed-in member's place
// in it: for those who are not invited, it doesn't exist.
func (s *Server) loadImportunity(c *gin.Context) (db.Importunity, db.ImportunityMember, bool) {
	var uri eventIDRequest
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return db.Importunity{}, db.ImportunityMember{}, false
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	member, err := s.store.GetImportunityMember(c, db.GetImportunityMemberParams{ImportunityID: uri.ID, UserID: authUser.ID})
	if errors.Is(err, sql.ErrNoRows) || err == nil && member.Status == "declined" {
		resourceNotFoundError(c, "Importunity")
		return db.Importunity{}, db.ImportunityMember{}, false
	}
	if err != nil {
		internalServerError(c, err)
		return db.Importunity{}, db.ImportunityMember{}, false
	}
	importunity, err := s.store.GetImportunity(c, uri.ID)
	if err != nil {
		internalServerError(c, err)
		return db.Importunity{}, db.ImportunityMember{}, false
	}
	return importunity, member, true
}

// GET /v1/prayer/importunities/:id
func (s *Server) getImportunity(c *gin.Context) {
	importunity, member, ok := s.loadImportunity(c)
	if !ok {
		return
	}
	s.sendImportunity(c, http.StatusOK, importunity, member)
}

// PUT /v1/prayer/importunities/:id: its author changes it, or closes it.
func (s *Server) updateImportunity(c *gin.Context) {
	importunity, member, ok := s.loadImportunity(c)
	if !ok {
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if importunity.OwnerID != authUser.ID {
		forbiddenError(c)
		return
	}
	var req importunityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	title, description, goal, err := req.check()
	if err != nil {
		badRequestError(c, err)
		return
	}
	status := req.Status
	if status == "" {
		status = importunity.Status
	}
	if status != "open" && status != "closed" {
		badRequestError(c, fmt.Errorf("status must be open or closed"))
		return
	}
	updated, err := s.store.UpdateImportunityTx(c, db.UpdateImportunityParams{
		ID: importunity.ID, Title: title, Description: description, Goal: goal, Status: status,
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	s.sendImportunity(c, http.StatusOK, updated, member)
}

// DELETE /v1/prayer/importunities/:id: its author removes it for everyone.
func (s *Server) deleteImportunity(c *gin.Context) {
	importunity, _, ok := s.loadImportunity(c)
	if !ok {
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if importunity.OwnerID != authUser.ID {
		forbiddenError(c)
		return
	}
	if _, err := s.store.DeleteImportunity(c, importunity.ID); err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}

type proclamationsRequest struct {
	Amount int64 `json:"amount" binding:"required"`
}

// POST /v1/prayer/importunities/:id/count: proclamations of the signed-in
// participant (the taps of the counter, grouped by the app).
func (s *Server) countProclamations(c *gin.Context) {
	importunity, _, ok := s.loadImportunity(c)
	if !ok {
		return
	}
	var req proclamationsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if req.Amount < 1 || req.Amount > maxProclamationsPerSend {
		badRequestError(c, fmt.Errorf("amount: 1 to %d", maxProclamationsPerSend))
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	updated, member, err := s.store.AddProclamationsTx(c, importunity.ID, authUser.ID, req.Amount)
	switch {
	case errors.Is(err, db.ErrImportunityNotFound):
		resourceNotFoundError(c, "Importunity")
		return
	case errors.Is(err, db.ErrImportunityClosed):
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "IMPORTUNITY_CLOSED", err))
		return
	case errors.Is(err, db.ErrNotParticipant):
		c.JSON(http.StatusForbidden, NewErrorResponse(http.StatusForbidden, "NOT_PARTICIPANT", err))
		return
	case err != nil:
		internalServerError(c, err)
		return
	}
	s.sendImportunity(c, http.StatusOK, updated, member)
}

// family is the spiritual family of a member: their disciple maker (FD),
// once the link was accepted, and the other disciples of the same FD.
func (s *Server) family(c *gin.Context, userID int64) (*memberCard, []int64, error) {
	discipleMaker, err := s.store.GetMyDiscipleMaker(c, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, []int64{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	disciples, err := s.store.ListDisciplesOf(c, nullID(discipleMaker))
	if err != nil {
		return nil, nil, err
	}
	members := []int64{discipleMaker}
	for _, id := range disciples {
		if id != userID && id != discipleMaker {
			members = append(members, id)
		}
	}
	cards, err := s.memberCards(c, []int64{discipleMaker})
	if err != nil {
		return nil, nil, err
	}
	card := cards[discipleMaker]
	return &card, members, nil
}

type familyResponse struct {
	// null: no disciple maker linked in the app
	DiscipleMaker *memberCard `json:"disciple_maker"`
	// how many would be invited: the FD and their other disciples (no names)
	Members int `json:"members"`
}

// GET /v1/prayer/importunities/family: whom "invite my spiritual family" reaches.
func (s *Server) getFamily(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	discipleMaker, members, err := s.family(c, authUser.ID)
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, familyResponse{DiscipleMaker: discipleMaker, Members: len(members)})
}

type invitationsRequest struct {
	Usernames []string `json:"usernames"`
	// the disciple maker of the signed-in member and their other disciples
	Family bool `json:"family"`
}

type invitationsResponse struct {
	// new invitations (those already invited are not counted)
	Invited int64 `json:"invited"`
}

// POST /v1/prayer/importunities/:id/invitations: a participant invites
// members by pseudo, or their spiritual family.
func (s *Server) inviteToImportunity(c *gin.Context) {
	importunity, member, ok := s.loadImportunity(c)
	if !ok {
		return
	}
	if member.Status != "joined" {
		c.JSON(http.StatusForbidden, NewErrorResponse(http.StatusForbidden, "NOT_PARTICIPANT", db.ErrNotParticipant))
		return
	}
	if importunity.Status != "open" {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "IMPORTUNITY_CLOSED", db.ErrImportunityClosed))
		return
	}
	var req invitationsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if len(req.Usernames) > maxInvitationsPerSend {
		badRequestError(c, fmt.Errorf("%d pseudos at most", maxInvitationsPerSend))
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	invited := []int64{}
	unknown := []string{}
	for _, username := range req.Usernames {
		user, err := s.store.GetUserByUsername(c, usernames.Normalize(username))
		if errors.Is(err, sql.ErrNoRows) {
			unknown = append(unknown, username)
			continue
		}
		if err != nil {
			internalServerError(c, err)
			return
		}
		if user.ID != authUser.ID {
			invited = append(invited, user.ID)
		}
	}
	if len(unknown) > 0 {
		c.JSON(http.StatusNotFound, NewErrorResponse(http.StatusNotFound, "USERNAME_NOT_FOUND",
			fmt.Errorf("unknown pseudos: %s", strings.Join(unknown, ", "))))
		return
	}
	if req.Family {
		_, members, err := s.family(c, authUser.ID)
		if err != nil {
			internalServerError(c, err)
			return
		}
		invited = append(invited, members...)
	}
	// those who blocked the inviter get nothing (and the inviter isn't told)
	invited, err := s.withoutBlockers(c, authUser.ID, invited)
	if err != nil {
		internalServerError(c, err)
		return
	}
	newcomers := []int64{}
	if len(invited) > 0 {
		var err error
		newcomers, err = s.store.InviteToImportunity(c, db.InviteToImportunityParams{
			ImportunityID: importunity.ID, InvitedBy: authUser.ID, UserIds: invited,
		})
		if err != nil {
			internalServerError(c, err)
			return
		}
	}
	if len(newcomers) > 0 {
		inviter := s.firstNameOf(c, authUser.ID)
		s.pushInvitation(importunity, inviter, newcomers)
	}
	apiResponse(c, http.StatusOK, invitationsResponse{Invited: int64(len(newcomers))})
}

type answerRequest struct {
	Join *bool `json:"join" binding:"required"`
}

// PUT /v1/prayer/importunities/:id/answer: the invited member joins, or declines.
func (s *Server) answerImportunity(c *gin.Context) {
	importunity, _, ok := s.loadImportunity(c)
	if !ok {
		return
	}
	var req answerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	member, err := s.store.AnswerImportunityInvitation(c, db.AnswerImportunityInvitationParams{
		Accept: *req.Join, ImportunityID: importunity.ID, UserID: authUser.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		resourceNotFoundError(c, "Invitation")
		return
	}
	if err != nil {
		internalServerError(c, err)
		return
	}
	if !*req.Join {
		apiResponse(c, http.StatusNoContent, nil)
		return
	}
	s.sendImportunity(c, http.StatusOK, importunity, member)
}

// DELETE /v1/prayer/importunities/:id/membership: a participant leaves; their
// proclamations stay in the total. The author deletes the topic instead.
func (s *Server) leaveImportunity(c *gin.Context) {
	importunity, _, ok := s.loadImportunity(c)
	if !ok {
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if importunity.OwnerID == authUser.ID {
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "OWNER_CANNOT_LEAVE",
			fmt.Errorf("the author deletes the topic instead")))
		return
	}
	if _, err := s.store.LeaveImportunity(c, db.LeaveImportunityParams{ImportunityID: importunity.ID, UserID: authUser.ID}); err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}
