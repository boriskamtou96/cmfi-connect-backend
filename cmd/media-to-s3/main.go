// Command media-to-s3 copies the images kept in the media folder (MEDIA_DIR)
// to the bucket (S3_*, Cloudflare R2 in production), once, when the images
// move to the CDN. Files already in the bucket are skipped, so it can run
// again after an interruption.
//
//	go run ./cmd/media-to-s3             // copy
//	go run ./cmd/media-to-s3 -dry-run    // only count
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/media"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "count the files to copy without sending them")
	flag.Parse()

	cfg, err := utils.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if cfg.Media.S3Bucket == "" {
		log.Fatal("S3_BUCKET is not set: nowhere to copy the images")
	}
	bucket, err := media.NewS3Store(cfg.Media.S3())
	if err != nil {
		log.Fatal(err)
	}
	entries, err := os.ReadDir(cfg.Media.Dir)
	if err != nil {
		log.Fatalf("media folder %s: %v", cfg.Media.Dir, err)
	}

	copied, skipped := 0, 0
	for _, entry := range entries {
		key := entry.Name()
		if entry.IsDir() || !media.ValidKey(key) {
			continue
		}
		there, err := bucket.Has(key)
		if err != nil {
			log.Fatalf("%s: %v", key, err)
		}
		if there {
			skipped++
			continue
		}
		if *dryRun {
			copied++
			continue
		}
		data, err := os.ReadFile(filepath.Join(cfg.Media.Dir, key))
		if err != nil {
			log.Fatalf("%s: %v", key, err)
		}
		if err := bucket.Save(key, data); err != nil {
			log.Fatalf("%s: %v", key, err)
		}
		copied++
	}
	if *dryRun {
		log.Printf("%d files to copy, %d already in the bucket", copied, skipped)
		return
	}
	log.Printf("%d files copied, %d already in the bucket", copied, skipped)
}
