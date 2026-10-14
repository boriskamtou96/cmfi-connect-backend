package api

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// ----- the profile photos of the members -----

// avatarOf is a member's photo as the apps show it, nil without one (the
// queries give id 0 when the member has none).
func avatarOf(m db.Medium) *mediaResponse {
	if m.ID == 0 {
		return nil
	}
	response := toMediaResponse(m)
	return &response
}

// userAvatar is the photo of member [userID], nil without one.
func (s *Server) userAvatar(c *gin.Context, userID int64) (*mediaResponse, error) {
	row, err := s.store.GetUserAvatar(c, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return avatarOf(row), nil
}

// dropMedia deletes an image no longer shown: its row, then its file. The
// request is done by then: a failure is only logged (a file left behind
// costs some disk, nothing more). An image still used elsewhere stays.
func (s *Server) dropMedia(c *gin.Context, id int64) {
	row, err := s.store.DeleteMedia(c, id)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("delete media %d: %v", id, err)
		}
		return
	}
	if err := s.media.Delete(row.Key); err != nil {
		log.Printf("delete media file %s: %v", row.Key, err)
	}
}

// avatarToDrop reads the photo of an account about to be deleted (0 without
// one), to drop once the account is gone. Answers 500 itself.
func (s *Server) avatarToDrop(c *gin.Context, userID int64) (int64, bool) {
	avatar, err := s.store.GetUserAvatar(c, userID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		internalServerError(c)
		return 0, false
	}
	return avatar.ID, true
}

// PUT /v1/users/me/avatar (multipart form, field "file"): a square JPEG or
// PNG of 2 MB at most, 128 to 2048 pixels a side. It is kept as a JPEG
// without its metadata (EXIF: place, camera) and replaces the previous one.
// 413 MEDIA_TOO_LARGE, 415 MEDIA_UNSUPPORTED, 400 AVATAR_INVALID.
func (s *Server) uploadMyAvatar(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, media.MaxAvatarSize+1<<20)
	header, err := c.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			avatarTooLargeError(c)
			return
		}
		badRequestError(c, err)
		return
	}
	if header.Size > media.MaxAvatarSize {
		avatarTooLargeError(c)
		return
	}
	file, err := header.Open()
	if err != nil {
		badRequestError(c, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, media.MaxAvatarSize+1))
	if err != nil {
		badRequestError(c, err)
		return
	}
	if len(data) > media.MaxAvatarSize {
		avatarTooLargeError(c)
		return
	}

	photo, info, err := media.Avatar(data)
	switch {
	case errors.Is(err, media.ErrAvatarShape), errors.Is(err, media.ErrAvatarSize):
		c.JSON(http.StatusBadRequest, NewErrorResponse(http.StatusBadRequest, "AVATAR_INVALID", err))
		return
	case errors.Is(err, media.ErrUnsupported):
		c.JSON(http.StatusUnsupportedMediaType, NewErrorResponse(http.StatusUnsupportedMediaType, "MEDIA_UNSUPPORTED",
			fmt.Errorf("profile photo: JPEG or PNG only")))
		return
	case err != nil:
		internalServerError(c)
		return
	}

	key := media.NewKey(info.Ext)
	if err := s.media.Save(key, photo); err != nil {
		internalServerError(c)
		return
	}
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	row, err := s.store.CreateMedia(c, db.CreateMediaParams{
		Key:         key,
		ContentType: info.ContentType,
		Width:       int32(info.Width),
		Height:      int32(info.Height),
		Color:       info.Color,
		SizeBytes:   int64(len(photo)),
		UploadedBy:  nullID(authUser.ID),
	})
	if err != nil {
		_ = s.media.Delete(key)
		internalServerError(c)
		return
	}

	previous, err := s.store.SetUserAvatar(c, db.SetUserAvatarParams{ID: authUser.ID, AvatarMediaID: nullID(row.ID)})
	if err != nil {
		s.dropMedia(c, row.ID)
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "user")
			return
		}
		internalServerError(c)
		return
	}
	if previous.Valid {
		s.dropMedia(c, previous.Int64)
	}
	apiResponse(c, http.StatusOK, toMediaResponse(row))
}

func avatarTooLargeError(c *gin.Context) {
	c.JSON(http.StatusRequestEntityTooLarge, NewErrorResponse(http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE",
		fmt.Errorf("profile photo up to %d MB", media.MaxAvatarSize>>20)))
}

// removeAvatar takes the photo of member [userID] off and deletes it.
// sql.ErrNoRows when the member doesn't exist; nothing to do without a photo.
func (s *Server) removeAvatar(c *gin.Context, userID int64) error {
	previous, err := s.store.SetUserAvatar(c, db.SetUserAvatarParams{ID: userID})
	if err != nil {
		return err
	}
	if previous.Valid {
		s.dropMedia(c, previous.Int64)
	}
	return nil
}

// DELETE /v1/users/me/avatar: the member takes their photo off (204, also
// without one).
func (s *Server) deleteMyAvatar(c *gin.Context) {
	authUser := c.MustGet(authorizationPayloadKey).(*token.Payload)
	if err := s.removeAvatar(c, authUser.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "user")
			return
		}
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}

// DELETE /v1/admin/members/:id/avatar: an admin takes off a photo that has
// no place in the community (204, also without one).
func (s *Server) adminDeleteMemberAvatar(c *gin.Context) {
	var uri getUserRequest
	if err := c.ShouldBindUri(&uri); err != nil {
		badRequestError(c, err)
		return
	}
	if err := s.removeAvatar(c, uri.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resourceNotFoundError(c, "member")
			return
		}
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusNoContent, nil)
}
