package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config: a bucket of an S3-compatible storage (Cloudflare R2 in
// production), whose files a CDN serves at the public address of the images.
type S3Config struct {
	// "https://<account id>.r2.cloudflarestorage.com"
	Endpoint        string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	// "auto" for R2
	Region string
}

// S3Store keeps the files in a bucket: the server keeps none, and the CDN in
// front of the bucket serves them.
type S3Store struct {
	client *minio.Client
	bucket string
}

// one request to the storage at most this long
const s3Timeout = 30 * time.Second

// NewS3Store connects to the bucket of [cfg] and checks that it can be
// reached.
func NewS3Store(cfg S3Config) (*S3Store, error) {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Host == "" {
		return nil, fmt.Errorf("S3_ENDPOINT %q: an address like https://<account>.r2.cloudflarestorage.com", cfg.Endpoint)
	}
	region := cfg.Region
	if region == "" {
		region = "auto"
	}
	client, err := minio.New(endpoint.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: endpoint.Scheme != "http",
		Region: region,
	})
	if err != nil {
		return nil, fmt.Errorf("S3 client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s3Timeout)
	defer cancel()
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("S3 bucket %s: %w", cfg.Bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("S3 bucket %s doesn't exist", cfg.Bucket)
	}
	return &S3Store{client: client, bucket: cfg.Bucket}, nil
}

// contentTypeOf gives the type of a file by its extension (the keys keep it).
func contentTypeOf(key string) string {
	for contentType, ext := range extensions {
		if filepath.Ext(key) == ext {
			return contentType
		}
	}
	return "application/octet-stream"
}

func (s *S3Store) Save(key string, data []byte) error {
	if !ValidKey(key) {
		return fmt.Errorf("invalid media key %q", key)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s3Timeout)
	defer cancel()
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentTypeOf(key),
		// the names are random and never reused: the CDN and the apps keep them
		CacheControl: "public, max-age=31536000, immutable",
	})
	return err
}

// Open reads a file through the API: only when no public address of the
// images is set (otherwise the API sends the apps to the CDN).
func (s *S3Store) Open(key string) (io.ReadSeekCloser, time.Time, error) {
	if !ValidKey(key) {
		return nil, time.Time{}, os.ErrNotExist
	}
	object, err := s.client.GetObject(context.Background(), s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, time.Time{}, err
	}
	stat, err := object.Stat()
	if err != nil {
		_ = object.Close()
		if minio.ToErrorResponse(err).Code == minio.NoSuchKey {
			return nil, time.Time{}, os.ErrNotExist
		}
		return nil, time.Time{}, err
	}
	return object, stat.LastModified, nil
}

func (s *S3Store) Delete(key string) error {
	if !ValidKey(key) {
		return fmt.Errorf("invalid media key %q", key)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s3Timeout)
	defer cancel()
	// a missing object is not an error for S3
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

// Has tells whether the bucket already has [key] (the copy of the old files
// skips them).
func (s *S3Store) Has(key string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s3Timeout)
	defer cancel()
	_, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if minio.ToErrorResponse(err).Code == minio.NoSuchKey {
		return false, nil
	}
	return false, err
}

// NewStore is the storage of the configuration: the bucket when one is set,
// else the folder [dir].
func NewStore(dir string, s3 S3Config) (Store, error) {
	if s3.Bucket != "" {
		return NewS3Store(s3)
	}
	if dir == "" {
		return nil, errors.New("no media storage: set MEDIA_DIR or S3_BUCKET")
	}
	return NewDiskStore(dir)
}
