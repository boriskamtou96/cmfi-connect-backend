// Package media keeps the images sent by the admins (posters, photos) and
// reads what the apps need to show them well: size, and a dominant color to
// paint while the image loads. It keeps PDF documents too (prayer topics).
package media

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	// the formats accepted: the apps send JPEG (compressed on the phone) or PNG
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// MaxSize is the largest image accepted. The apps compress before sending.
const MaxSize = 8 << 20

// MaxDocumentSize is the largest PDF accepted.
const MaxDocumentSize = 10 << 20

// MaxAvatarSize is the largest profile photo accepted. The apps crop and
// compress it before sending.
const MaxAvatarSize = 2 << 20

// The side of a profile photo, in pixels.
const (
	MinAvatarSide = 128
	MaxAvatarSide = 2048
)

// ErrUnsupported: not a JPEG, a PNG nor a PDF, or an image that can't be read.
var ErrUnsupported = errors.New("unsupported file: JPEG, PNG or PDF only")

var (
	// ErrAvatarShape: a profile photo is square (2 pixels of difference at most).
	ErrAvatarShape = errors.New("profile photo: the picture must be square")
	// ErrAvatarSize: a profile photo too small to be sharp, or too large.
	ErrAvatarSize = fmt.Errorf("profile photo: %d to %d pixels a side", MinAvatarSide, MaxAvatarSide)
)

// Info is what is kept about an image. A document has no size nor color.
type Info struct {
	ContentType string
	Width       int
	Height      int
	// "#RRGGBB", the average of the picture
	Color string
	Ext   string
}

var extensions = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "application/pdf": ".pdf"}

// IsDocument: a PDF, not an image.
func (i Info) IsDocument() bool { return i.ContentType == "application/pdf" }

// Inspect checks that [data] is an image the apps can show and measures it,
// or a PDF document.
func Inspect(data []byte) (Info, error) {
	contentType := http.DetectContentType(data)
	ext, ok := extensions[contentType]
	if !ok {
		return Info{}, ErrUnsupported
	}
	if contentType == "application/pdf" {
		return Info{ContentType: contentType, Ext: ext}, nil
	}
	picture, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Info{}, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	bounds := picture.Bounds()
	return Info{
		ContentType: contentType,
		Width:       bounds.Dx(),
		Height:      bounds.Dy(),
		Color:       averageColor(picture),
		Ext:         ext,
	}, nil
}

// Avatar checks a profile photo (a square JPEG or PNG) and makes it again as
// a JPEG: what the phone wrote in the file (EXIF: place, camera) stays out.
// A transparent PNG gets a white background.
func Avatar(data []byte) ([]byte, Info, error) {
	contentType := http.DetectContentType(data)
	if contentType != "image/jpeg" && contentType != "image/png" {
		return nil, Info{}, ErrUnsupported
	}
	// the size first, read from the header: no pixels decoded for a picture
	// refused anyway
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, Info{}, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if abs(config.Width-config.Height) > 2 {
		return nil, Info{}, ErrAvatarShape
	}
	if min(config.Width, config.Height) < MinAvatarSide || max(config.Width, config.Height) > MaxAvatarSide {
		return nil, Info{}, ErrAvatarSize
	}
	picture, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, Info{}, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	bounds := picture.Bounds()
	if contentType == "image/png" {
		flat := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
		draw.Draw(flat, flat.Bounds(), image.White, image.Point{}, draw.Src)
		draw.Draw(flat, flat.Bounds(), picture, bounds.Min, draw.Over)
		picture = flat
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, picture, &jpeg.Options{Quality: 85}); err != nil {
		return nil, Info{}, err
	}
	return out.Bytes(), Info{
		ContentType: "image/jpeg",
		Width:       bounds.Dx(),
		Height:      bounds.Dy(),
		Color:       averageColor(picture),
		Ext:         ".jpg",
	}, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// averageColor reads a grid of about 48×48 points: enough for a background color.
func averageColor(picture image.Image) string {
	bounds := picture.Bounds()
	stepX := max(1, bounds.Dx()/48)
	stepY := max(1, bounds.Dy()/48)
	var r, g, b, n uint64
	for y := bounds.Min.Y; y < bounds.Max.Y; y += stepY {
		for x := bounds.Min.X; x < bounds.Max.X; x += stepX {
			pr, pg, pb, _ := picture.At(x, y).RGBA()
			r, g, b, n = r+uint64(pr>>8), g+uint64(pg>>8), b+uint64(pb>>8), n+1
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("#%02X%02X%02X", r/n, g/n, b/n)
}

// NewKey names a file: random, so the links can't be guessed one from another.
func NewKey(ext string) string {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes) + ext
}

var keyFormat = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png|pdf)$`)

// ValidKey refuses anything that isn't a name made by NewKey (no "../").
func ValidKey(key string) bool {
	return keyFormat.MatchString(key)
}

// Store keeps the files. On disk for now; an object storage (Cloudflare R2,
// S3, MinIO) can take its place without changing the API.
type Store interface {
	Save(key string, data []byte) error
	// Open gives the file to serve, and when it was written.
	Open(key string) (io.ReadSeekCloser, time.Time, error)
	// Delete removes a file; one already gone is not an error.
	Delete(key string) error
}

// DiskStore keeps the files in a folder of the server.
type DiskStore struct {
	Dir string
}

// NewDiskStore creates [dir] if needed.
func NewDiskStore(dir string) (*DiskStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("media folder %s: %w", dir, err)
	}
	return &DiskStore{Dir: dir}, nil
}

func (s *DiskStore) Save(key string, data []byte) error {
	if !ValidKey(key) {
		return fmt.Errorf("invalid media key %q", key)
	}
	return os.WriteFile(filepath.Join(s.Dir, key), data, 0o644)
}

func (s *DiskStore) Open(key string) (io.ReadSeekCloser, time.Time, error) {
	if !ValidKey(key) {
		return nil, time.Time{}, os.ErrNotExist
	}
	file, err := os.Open(filepath.Join(s.Dir, key))
	if err != nil {
		return nil, time.Time{}, err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, time.Time{}, err
	}
	return file, stat.ModTime(), nil
}

func (s *DiskStore) Delete(key string) error {
	if !ValidKey(key) {
		return fmt.Errorf("invalid media key %q", key)
	}
	err := os.Remove(filepath.Join(s.Dir, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
