package api

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
)

// ----- the members, for the back office -----

// adminMemberResponse is what the back office shows of an account: who, where,
// since when, the last time the app was opened. No spiritual data (reports,
// prayer lists).
type adminMemberResponse struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Role        string     `json:"role"`
	FirstName   string     `json:"first_name"`
	LastName    string     `json:"last_name"`
	PhoneNumber string     `json:"phone_number"`
	Email       string     `json:"email"`
	Country     string     `json:"country"`
	City        string     `json:"city"`
	Church      string     `json:"church"`
	Assembly    string     `json:"assembly"`
	CreatedAt   time.Time  `json:"created_at"`
	LastSeenAt  *time.Time `json:"last_seen_at"`
	// phones that get the push
	Phones int32 `json:"phones"`
	// the profile photo, null without one
	Avatar *mediaResponse `json:"avatar"`
}

func toAdminMember(row db.ListAdminMembersRow) adminMemberResponse {
	member := adminMemberResponse{
		ID:          row.ID,
		Username:    row.Username,
		Role:        row.Role,
		FirstName:   row.FirstName,
		LastName:    row.LastName,
		PhoneNumber: row.PhoneNumber,
		Email:       row.Email,
		Country:     strings.ToUpper(strings.TrimSpace(row.Country)),
		City:        row.City,
		Church:      row.Church,
		Assembly:    row.Assembly,
		CreatedAt:   row.CreatedAt,
		Phones:      row.Phones,
		Avatar: avatarOf(db.Medium{
			ID:     row.AvatarID,
			Key:    row.AvatarKey,
			Width:  row.AvatarWidth,
			Height: row.AvatarHeight,
			Color:  row.AvatarColor,
		}),
	}
	if seen, ok := row.LastSeenAt.(time.Time); ok {
		member.LastSeenAt = &seen
	}
	return member
}

type adminMembersRequest struct {
	pageRequest
	// a name, a pseudo or digits of the phone number
	Search   string `form:"search"`
	Country  string `form:"country"`
	City     string `form:"city"`
	Church   string `form:"church"`
	Assembly string `form:"assembly"`
	Role     string `form:"role"`
}

var nonDigits = regexp.MustCompile(`\D`)

// keeps tells whether a member answers the search and the filters; the
// places are compared like the audience of a notification (case and accents
// left aside).
func (r adminMembersRequest) keeps(m adminMemberResponse) bool {
	places := audienceFilter{Country: r.Country, City: r.City, Church: r.Church, Assembly: r.Assembly}.trimmed().places()
	if !matches([placeCount]string{m.Country, m.City, m.Church, m.Assembly}, places, -1) {
		return false
	}
	if role := strings.TrimSpace(r.Role); role != "" && role != m.Role {
		return false
	}
	search := fold(r.Search)
	if search == "" {
		return true
	}
	// "paul ndi paul" finds "Paul Ndi" and "Ndi Paul"
	names := fold(strings.Join([]string{m.FirstName, m.LastName, m.FirstName, m.Username}, " "))
	if strings.Contains(names, strings.TrimPrefix(search, "@")) {
		return true
	}
	digits := nonDigits.ReplaceAllString(search, "")
	return len(digits) >= 3 && strings.Contains(nonDigits.ReplaceAllString(m.PhoneNumber, ""), digits)
}

// GET /v1/admin/members?search=&country=&city=&church=&assembly=&role=&page=&size=:
// the accounts, the newest first, a page at a time.
func (s *Server) listAdminMembers(c *gin.Context) {
	var req adminMembersRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}
	rows, err := s.store.ListAdminMembers(c)
	if err != nil {
		internalServerError(c)
		return
	}
	kept := []adminMemberResponse{}
	for _, row := range rows {
		if member := toAdminMember(row); req.keeps(member) {
			kept = append(kept, member)
		}
	}
	limit, offset := req.limits()
	page := kept[min(int(offset), len(kept)):min(int(offset+limit), len(kept))]
	paginatedApiResponse(c, http.StatusOK, page, MetaResponse{
		Total:  int64(len(kept)),
		Limit:  limit,
		Offset: offset,
	})
}

// GET /v1/admin/members/:id: one account.
func (s *Server) getAdminMember(c *gin.Context) {
	var uri getUserRequest
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	row, err := s.store.GetAdminMember(c, uri.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "member")
			return
		}
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusOK, toAdminMember(db.ListAdminMembersRow(row)))
}
