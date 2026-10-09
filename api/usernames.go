package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/usernames"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

// How many free pseudos are offered at a time.
const suggestionCount = 3

// memberCard is what any signed-in member sees of another, found by pseudo:
// never the phone number nor the e-mail.
type memberCard struct {
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Church    string `json:"church"`
	Assembly  string `json:"assembly"`
}

func toMemberCard(card db.ListMemberCardsRow) memberCard {
	return memberCard{
		Username:  card.Username,
		FirstName: card.FirstName,
		LastName:  card.LastName,
		Church:    card.Church,
		Assembly:  card.Assembly,
	}
}

// freeSuggestions are pseudos made from a name that nobody uses yet.
func (s *Server) freeSuggestions(c *gin.Context, firstName, lastName string) ([]string, error) {
	candidates := usernames.Candidates(firstName, lastName)
	taken, err := s.store.ListTakenUsernames(c, candidates)
	if err != nil {
		return nil, err
	}
	used := make(map[string]bool, len(taken))
	for _, username := range taken {
		used[username] = true
	}
	free := make([]string, 0, suggestionCount)
	for _, candidate := range candidates {
		if !used[candidate] {
			free = append(free, candidate)
			if len(free) == suggestionCount {
				break
			}
		}
	}
	return free, nil
}

type usernameAvailabilityRequest struct {
	Username  string `form:"username"`
	FirstName string `form:"first_name"`
	LastName  string `form:"last_name"`
}

type usernameAvailabilityResponse struct {
	Username  string `json:"username"`
	Available bool   `json:"available"`
	// "invalid", "reserved" or "taken" when not available
	Problem     usernames.Problem `json:"problem,omitempty"`
	Suggestions []string          `json:"suggestions"`
}

// GET /v1/usernames/availability?username=&first_name=&last_name=
// For the sign-up form: is this pseudo free, and which ones are (from the name).
// Without username, only the suggestions.
func (s *Server) usernameAvailability(c *gin.Context) {
	var req usernameAvailabilityRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		badRequestError(c, err)
		return
	}

	response := usernameAvailabilityResponse{Username: usernames.Normalize(req.Username)}
	if response.Username != "" {
		response.Problem = usernames.Check(response.Username)
		if response.Problem == "" {
			_, err := s.store.GetUserByUsername(c, response.Username)
			switch {
			case err == nil:
				response.Problem = usernames.Taken
			case !errors.Is(err, sql.ErrNoRows):
				internalServerError(c)
				return
			}
		}
		response.Available = response.Problem == ""
	}

	suggestions, err := s.freeSuggestions(c, req.FirstName, req.LastName)
	if err != nil {
		internalServerError(c)
		return
	}
	response.Suggestions = suggestions

	apiResponse(c, http.StatusOK, response)
}

type usernameRequest struct {
	Username string `uri:"username" binding:"required"`
}

// GET /v1/users/by-username/:username
func (s *Server) getMemberByUsername(c *gin.Context) {
	var req usernameRequest
	if err := c.ShouldBindUri(&req); err != nil {
		badRequestError(c, err)
		return
	}

	card, err := s.memberCardByUsername(c, usernames.Normalize(req.Username))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "member")
			return
		}
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusOK, toMemberCard(card))
}

// memberCardByUsername returns sql.ErrNoRows when nobody has this pseudo.
func (s *Server) memberCardByUsername(c *gin.Context, username string) (db.ListMemberCardsRow, error) {
	user, err := s.store.GetUserByUsername(c, username)
	if err != nil {
		return db.ListMemberCardsRow{}, err
	}
	cards, err := s.store.ListMemberCards(c, []int64{user.ID})
	if err != nil {
		return db.ListMemberCardsRow{}, err
	}
	if len(cards) == 0 {
		return db.ListMemberCardsRow{}, sql.ErrNoRows
	}
	return cards[0], nil
}

type updateUsernameRequest struct {
	Username string `json:"username" binding:"required"`
}

// PUT /v1/users/me/username
func (s *Server) updateMyUsername(c *gin.Context) {
	var req updateUsernameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequestError(c, err)
		return
	}
	username := usernames.Normalize(req.Username)
	if problem := usernames.Check(username); problem != "" {
		usernameError(c, problem)
		return
	}

	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	user, err := s.store.UpdateUsername(c, db.UpdateUsernameParams{ID: authUser.ID, Username: username})
	if err != nil {
		if isUniqueViolation(err, "users_username_key") {
			usernameError(c, usernames.Taken)
			return
		}
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusOK, convertToUserResponse(user))
}

// usernameError: 400 USERNAME_INVALID / USERNAME_RESERVED, 409 USERNAME_TAKEN.
func usernameError(c *gin.Context, problem usernames.Problem) {
	switch problem {
	case usernames.Taken:
		c.JSON(http.StatusConflict, NewErrorResponse(http.StatusConflict, "USERNAME_TAKEN", fmt.Errorf("username already used")))
	case usernames.Reserved:
		c.JSON(http.StatusBadRequest, NewErrorResponse(http.StatusBadRequest, "USERNAME_RESERVED", fmt.Errorf("username reserved")))
	default:
		c.JSON(http.StatusBadRequest, NewErrorResponse(http.StatusBadRequest, "USERNAME_INVALID",
			fmt.Errorf("username: %d to %d characters among a-z, 0-9, \".\" and \"_\"", usernames.MinLength, usernames.MaxLength)))
	}
}

// isUniqueViolation tells whether err breaks the unique constraint or index [name].
func isUniqueViolation(err error, name string) bool {
	pgErr, ok := errors.AsType[*pq.Error](err)
	return ok && pgErr.Code.Name() == "unique_violation" && pgErr.Constraint == name
}
