package api

import (
	"context"
	"log/slog"
	"time"

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
)

// compressTimeout: two hours of sound on a small server.
const compressTimeout = 20 * time.Minute

// compressLater re-encodes the sound [id] (kept at [key]) for the phones,
// after the admin got their answer: on a small server two hours of sound
// take minutes. One at a time. The smaller file replaces the first under
// the same media; kept as sent when ffmpeg is missing, fails, or gains
// less than a tenth.
func (s *Server) compressLater(id int64, key string, data []byte) {
	if s.audio == nil {
		return
	}
	s.compressing.Add(1)
	go func() {
		defer s.compressing.Done()
		s.compressSlot <- struct{}{}
		defer func() { <-s.compressSlot }()

		ctx, cancel := context.WithTimeout(context.Background(), compressTimeout)
		defer cancel()
		start := time.Now()
		smaller, err := s.audio.Compress(ctx, data)
		if err != nil {
			slog.Error("sound not compressed", "media_id", id, "error", err)
			return
		}
		if len(smaller) == 0 || len(smaller) >= len(data)*9/10 {
			slog.Info("sound kept as sent", "media_id", id, "bytes", len(data), "compressed", len(smaller))
			return
		}
		if _, err := media.InspectAudio(smaller, "compressed.m4a"); err != nil {
			slog.Error("compressed sound unreadable", "media_id", id, "error", err)
			return
		}
		newKey := media.NewKey(".m4a")
		if err := s.media.Save(newKey, smaller); err != nil {
			slog.Error("compressed sound not saved", "media_id", id, "error", err)
			return
		}
		replaced, err := s.store.ReplaceMediaFile(ctx, db.ReplaceMediaFileParams{
			ID:          id,
			Key:         newKey,
			ContentType: media.M4A,
			SizeBytes:   int64(len(smaller)),
		})
		if err != nil || replaced == 0 {
			// deleted meanwhile, or not written: the new file goes
			if err != nil {
				slog.Error("compressed sound not recorded", "media_id", id, "error", err)
			}
			_ = s.media.Delete(newKey)
			return
		}
		if err := s.media.Delete(key); err != nil {
			slog.Error("first sound not deleted", "media_id", id, "error", err)
		}
		slog.Info("sound compressed", "media_id", id, "bytes", len(data), "compressed", len(smaller),
			"took", time.Since(start).Round(time.Millisecond))
	}()
}
