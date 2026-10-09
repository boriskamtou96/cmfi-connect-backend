package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/usernames"
	"github.com/gin-gonic/gin"
)

// staffMember is a member with a role, as the superadmin manages them.
type staffMember struct {
	memberCard
	Role string `json:"role"`
}

// GET /v1/admin/staff: the admins and superadmins (for admins and superadmins).
func (s *Server) listStaff(c *gin.Context) {
	users, err := s.store.ListUsersByRoles(c, []string{roleAdmin, roleSuperadmin})
	if err != nil {
		internalServerError(c)
		return
	}
	ids := make([]int64, len(users))
	for i, user := range users {
		ids[i] = user.ID
	}
	cards, err := s.store.ListMemberCards(c, ids)
	if err != nil {
		internalServerError(c)
		return
	}
	byID := make(map[int64]db.ListMemberCardsRow, len(cards))
	for _, card := range cards {
		byID[card.ID] = card
	}

	staff := make([]staffMember, 0, len(users))
	for _, user := range users {
		staff = append(staff, staffMember{memberCard: toMemberCard(byID[user.ID]), Role: user.Role})
	}
	apiResponse(c, http.StatusOK, staff)
}

type setRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

// PUT /v1/admin/members/:username/role (superadmins only). A superadmin can't
// change their own role: the app always keeps one.
func (s *Server) setMemberRole(c *gin.Context) {
	var uri usernameRequest
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	var req setRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	if !slices.Contains([]string{roleUser, roleAdmin, roleSuperadmin}, req.Role) {
		badRequestError(c, fmt.Errorf("role must be user, admin or superadmin"))
		return
	}

	member, err := s.store.GetUserByUsername(c, usernames.Normalize(uri.Username))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "member")
			return
		}
		internalServerError(c)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if member.ID == authUser.ID {
		badRequestError(c, fmt.Errorf("a superadmin can't change their own role"))
		return
	}

	updated, err := s.store.UpdateUserRole(c, db.UpdateUserRoleParams{ID: member.ID, Role: req.Role})
	if err != nil {
		internalServerError(c)
		return
	}
	card, err := s.memberCardByUsername(c, updated.Username)
	if err != nil {
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusOK, staffMember{memberCard: toMemberCard(card), Role: updated.Role})
}
