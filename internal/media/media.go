// Package media keeps the images sent by the admins (posters, photos) and
// reads what the apps need to show them well: size, and a dominant color to
// paint while the image loads. It keeps PDF documents too (prayer topics),
// and Word files for Nos documents (InspectDocument), and the sounds of the
// Enseignements (InspectAudio).
package media

import (
	"archive/zip"
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
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MaxSize is the largest image accepted. The apps compress before sending.
const MaxSize = 8 << 20

// MaxDocumentSize is the largest PDF accepted.
const MaxDocumentSize = 10 << 20

// MaxAudioSize is the largest sound accepted: a message of two hours at
// 96 kbit/s.
const MaxAudioSize = 100 << 20

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

// The documents kept: a PDF, or a Word file (only for Nos documents).
const (
	PDF  = "application/pdf"
	DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	DOC  = "application/msword"
)

// The sounds kept (only for the Enseignements).
const (
	MP3 = "audio/mpeg"
	M4A = "audio/mp4"
)

var extensions = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png",
	PDF: ".pdf", DOCX: ".docx", DOC: ".doc",
	MP3: ".mp3", M4A: ".m4a",
}

// the Word files are served with their own type (http.ServeContent reads it
// from the extension), not as a zip
func init() {
	_ = mime.AddExtensionType(".docx", DOCX)
	_ = mime.AddExtensionType(".doc", DOC)
	_ = mime.AddExtensionType(".m4a", M4A)
}

// ErrUnsupportedDocument: not a PDF nor a Word file.
var ErrUnsupportedDocument = errors.New("unsupported document: PDF or Word (.docx, .doc) only")

// IsDocument: a PDF or a Word file, not an image.
func (i Info) IsDocument() bool {
	return i.ContentType == PDF || i.ContentType == DOCX || i.ContentType == DOC
}

// ErrUnsupportedAudio: not an MP3 nor an M4A.
var ErrUnsupportedAudio = errors.New("unsupported sound: MP3 or M4A only")

// IsAudio: an MP3 or an M4A.
func (i Info) IsAudio() bool {
	return i.ContentType == MP3 || i.ContentType == M4A
}

// InspectAudio checks that [data] is a sound the phones play: an MP3 (its
// ID3 tag, or straight its first MPEG frame: layer III) or an M4A (an MP4
// box "ftyp" of an audio brand, or any MP4 named .m4a). A video, or AAC
// without its MP4 box, is refused.
func InspectAudio(data []byte, name string) (Info, error) {
	switch {
	case bytes.HasPrefix(data, []byte("ID3")):
		return Info{ContentType: MP3, Ext: extensions[MP3]}, nil
	// the frame sync (11 bits), MPEG 1, 2 or 2.5, layer III
	case len(data) >= 2 && data[0] == 0xFF && data[1]&0xE0 == 0xE0 && data[1]&0x18 != 0x08 && data[1]&0x06 == 0x02:
		return Info{ContentType: MP3, Ext: extensions[MP3]}, nil
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		brand := string(data[8:12])
		if brand == "M4A " || brand == "M4B " || strings.EqualFold(filepath.Ext(name), ".m4a") {
			return Info{ContentType: M4A, Ext: extensions[M4A]}, nil
		}
	}
	return Info{}, ErrUnsupportedAudio
}

// oleHeader starts the old Office files (.doc, but .xls and .ppt as well).
var oleHeader = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// InspectDocument checks that [data] is a PDF or a Word file: a .docx (a
// zip holding word/document.xml) or an old .doc (an Office file whose
// [name] ends in .doc). Inspect, used for everything else, still refuses
// Word files: a poster or a photo can't be one.
func InspectDocument(data []byte, name string) (Info, error) {
	if http.DetectContentType(data) == PDF {
		return Info{ContentType: PDF, Ext: extensions[PDF]}, nil
	}
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		if archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err == nil {
			for _, file := range archive.File {
				if file.Name == "word/document.xml" {
					return Info{ContentType: DOCX, Ext: extensions[DOCX]}, nil
				}
			}
		}
	}
	if bytes.HasPrefix(data, oleHeader) && strings.EqualFold(filepath.Ext(name), ".doc") {
		return Info{ContentType: DOC, Ext: extensions[DOC]}, nil
	}
	return Info{}, ErrUnsupportedDocument
}

// Inspect checks that [data] is an image the apps can show and measures it,
// or a PDF document.
func Inspect(data []byte) (Info, error) {
	contentType := http.DetectContentType(data)
	ext, ok := extensions[contentType]
	if !ok {
		return Info{}, ErrUnsupported
	}
	if contentType == PDF {
		return Info{ContentType: contentType, Ext: ext}, nil
	}
	// a sound goes through InspectAudio, for the Enseignements only
	if !strings.HasPrefix(contentType, "image/") {
		return Info{}, ErrUnsupported
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

var keyFormat = regexp.MustCompile(`^[0-9a-f]{32}\.(jpg|png|pdf|docx|doc|mp3|m4a)$`)

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
