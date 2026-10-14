package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/token"
	"github.com/gin-gonic/gin"
)

// apiMediaPrefix: the API serves the images itself (development, tests).
const apiMediaPrefix = "/v1/media/"

// mediaPrefix starts the links of the images: the CDN in front of the bucket
// when MEDIA_PUBLIC_URL is set ("https://media.cmfi-connect.com/"), else the
// API. Set once, when the server is made (New).
var mediaPrefix = apiMediaPrefix

// mediaURL is the link of the file [key], as the apps fetch it.
func mediaURL(key string) string { return mediaPrefix + key }

// mediaResponse is an image as the apps show it: [url] is the CDN's address,
// or relative to the API host ("/v1/media/…"); [color] is painted while it
// loads.
type mediaResponse struct {
	ID     int64  `json:"id"`
	URL    string `json:"url"`
	Width  int32  `json:"width"`
	Height int32  `json:"height"`
	Color  string `json:"color"`
}

func toMediaResponse(m db.Medium) mediaResponse {
	return mediaResponse{ID: m.ID, URL: mediaURL(m.Key), Width: m.Width, Height: m.Height, Color: m.Color}
}

// mediaByIDs loads the images of [ids] (zeros ignored), by id.
func (s *Server) mediaByIDs(c *gin.Context, ids []int64) (map[int64]mediaResponse, error) {
	wanted := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id != 0 {
			wanted = append(wanted, id)
		}
	}
	found := map[int64]mediaResponse{}
	if len(wanted) == 0 {
		return found, nil
	}
	rows, err := s.store.ListMediaByIDs(c, wanted)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		found[row.ID] = toMediaResponse(row)
	}
	return found, nil
}

// POST /v1/admin/media (multipart form, field "file"): a JPEG or PNG of 8 MB
// at most, or a PDF of 10 MB.
func (s *Server) uploadMedia(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, media.MaxDocumentSize+1<<20)
	header, err := c.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			mediaTooLargeError(c)
			return
		}
		badRequestError(c, err)
		return
	}
	if header.Size > media.MaxDocumentSize {
		mediaTooLargeError(c)
		return
	}
	file, err := header.Open()
	if err != nil {
		badRequestError(c, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, media.MaxDocumentSize+1))
	if err != nil {
		badRequestError(c, err)
		return
	}

	info, err := media.Inspect(data)
	if err != nil {
		c.JSON(http.StatusUnsupportedMediaType, NewErrorResponse(http.StatusUnsupportedMediaType, "MEDIA_UNSUPPORTED", err))
		return
	}
	if !info.IsDocument() && len(data) > media.MaxSize {
		mediaTooLargeError(c)
		return
	}
	key := media.NewKey(info.Ext)
	if err := s.media.Save(key, data); err != nil {
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
		SizeBytes:   int64(len(data)),
		UploadedBy:  nullID(authUser.ID),
	})
	if err != nil {
		internalServerError(c)
		return
	}
	apiResponse(c, http.StatusCreated, toMediaResponse(row))
}

func mediaTooLargeError(c *gin.Context) {
	c.JSON(http.StatusRequestEntityTooLarge, NewErrorResponse(http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE",
		fmt.Errorf("images up to %d MB, PDF up to %d MB", media.MaxSize>>20, media.MaxDocumentSize>>20)))
}

// GET /v1/media/:key: public, like a link to an image. The names are random
// and never reused, so the apps may keep the file forever. With a CDN, the
// links given before it (still kept by the apps) lead there.
func (s *Server) serveMedia(c *gin.Context) {
	key := c.Param("key")
	if mediaPrefix != apiMediaPrefix && media.ValidKey(key) {
		c.Redirect(http.StatusMovedPermanently, mediaURL(key))
		return
	}
	file, modTime, err := s.media.Open(key)
	if err != nil {
		resourceNotFoundError(c, "media")
		return
	}
	defer file.Close()
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(c.Writer, c.Request, key, modTime, file)
}
