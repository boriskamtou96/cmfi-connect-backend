package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// The sounds of the teachings are re-encoded for the members' phones: AAC,
// mono, 64 kb/s, in an M4A. A voice keeps all its clarity, and ten minutes
// weigh about 4.7 MB instead of ~10: quicker to stream and to keep on the
// phone. Done by ffmpeg (FFMPEG_PATH, else found in the PATH); without it,
// the sounds stay as sent.

// speechBitrate: plenty for a voice.
const speechBitrate = "64k"

// AudioCompressor re-encodes a sound smaller.
type AudioCompressor interface {
	Compress(ctx context.Context, data []byte) ([]byte, error)
}

// FFmpeg compresses with the ffmpeg program at [Path].
type FFmpeg struct {
	Path string
}

// FindFFmpeg is ffmpeg at [path], or the one of the PATH; nil without it.
func FindFFmpeg(path string) AudioCompressor {
	if path == "" {
		found, err := exec.LookPath("ffmpeg")
		if err != nil {
			return nil
		}
		path = found
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return &FFmpeg{Path: path}
}

// Compress gives [data] as an M4A, AAC mono at speechBitrate, its index at
// the start so the phones play it while it downloads.
func (f *FFmpeg) Compress(ctx context.Context, data []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "cmfi-audio-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in")
	out := filepath.Join(dir, "out.m4a")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, f.Path,
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", in,
		"-vn", "-ac", "1", "-c:a", "aac", "-b:a", speechBitrate,
		"-movflags", "+faststart",
		out,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, output)
	}
	return os.ReadFile(out)
}
