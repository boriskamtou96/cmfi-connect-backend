package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/games"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// The leaderboard of the games: the points of those who chose to appear,
// this week or since ever, among all or in the member's circle.

type gameLeaderboardEntry struct {
	Rank     int            `json:"rank"`
	Username string         `json:"username"`
	Avatar   *mediaResponse `json:"avatar"`
	Points   int            `json:"points"`
	// the line of the member who asks
	Me bool `json:"me"`
}

type gameStandingResponse struct {
	Rank   int `json:"rank"`
	Points int `json:"points"`
}

type gameLeaderboardResponse struct {
	Period  string                 `json:"period"`
	Scope   string                 `json:"scope"`
	Entries []gameLeaderboardEntry `json:"entries"`
	// where the member stands, null when they are not in it (they said no,
	// have not chosen yet, or have no point over the period)
	Me *gameStandingResponse `json:"me"`
}

// GET /v1/games/leaderboard?period=week|all&scope=all|circle: the 50 first of
// those who chose to appear, and where the member stands. The week is the one
// of Douala; the circle is the member, their authorities and their disciples.
func (s *Server) getGameLeaderboard(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	period, scope := c.DefaultQuery("period", "week"), c.DefaultQuery("scope", "all")
	now := gameClock()

	var since sql.NullTime
	switch period {
	case "week":
		since = sql.NullTime{Time: games.WeekStart(now), Valid: true}
	case "all":
	default:
		badRequestError(c, fmt.Errorf("period: week or all"))
		return
	}
	var ids []int64
	switch scope {
	case "all":
	case "circle":
		circle, err := s.store.ListGameCircleIDs(c, authUser.ID)
		if err != nil {
			internalServerError(c, err)
			return
		}
		ids = append(circle, authUser.ID)
	default:
		badRequestError(c, fmt.Errorf("scope: all or circle"))
		return
	}

	rows, err := s.store.ListGameLeaderboard(c, db.ListGameLeaderboardParams{Since: since, Ids: ids, ViewerID: authUser.ID})
	if err != nil {
		internalServerError(c, err)
		return
	}
	response := gameLeaderboardResponse{Period: period, Scope: scope, Entries: make([]gameLeaderboardEntry, 0, len(rows))}
	for i, row := range rows {
		avatar, err := s.userAvatar(c, row.ID)
		if err != nil {
			internalServerError(c, err)
			return
		}
		response.Entries = append(response.Entries, gameLeaderboardEntry{
			Rank: i + 1, Username: row.Username, Avatar: avatar, Points: int(row.Points), Me: row.ID == authUser.ID,
		})
	}

	player, err := s.store.GetGamePlayer(c, authUser.ID)
	if err == nil && player.InLeaderboard.Valid && player.InLeaderboard.Bool {
		rank, points, err := s.gameRank(c, authUser.ID, since, ids)
		if err != nil {
			internalServerError(c, err)
			return
		}
		if rank != nil {
			response.Me = &gameStandingResponse{Rank: *rank, Points: points}
		}
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, response)
}

type gameSettingsRequest struct {
	InLeaderboard *bool `json:"in_leaderboard"`
	DailyGoal     *int  `json:"daily_goal"`
}

// PUT /v1/games/me: the choices of the player (to appear in the leaderboard,
// the goal of the day); what is not sent stays.
func (s *Server) updateGamePlayer(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	var request gameSettingsRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		badRequestError(c, err)
		return
	}
	if goal := request.DailyGoal; goal != nil && *goal != 30 && *goal != 50 && *goal != 100 {
		badRequestError(c, fmt.Errorf("daily_goal: 30, 50 or 100"))
		return
	}

	err := s.store.WithGamePlayerTx(c, authUser.ID, func(q *db.Queries, player db.GamePlayer) error {
		settings := db.SaveGamePlayerSettingsParams{
			UserID: authUser.ID, InLeaderboard: player.InLeaderboard, DailyGoal: player.DailyGoal,
		}
		if request.InLeaderboard != nil {
			settings.InLeaderboard = sql.NullBool{Bool: *request.InLeaderboard, Valid: true}
		}
		if request.DailyGoal != nil {
			settings.DailyGoal = int32(*request.DailyGoal)
		}
		return q.SaveGamePlayerSettings(c, settings)
	})
	if err != nil {
		internalServerError(c, err)
		return
	}
	player, err := s.gamePlayer(c, authUser.ID, gameClock())
	if err != nil {
		internalServerError(c, err)
		return
	}
	apiResponse(c, http.StatusOK, player)
}
