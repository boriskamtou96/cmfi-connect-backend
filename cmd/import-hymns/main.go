// Command import-hymns loads hymn books into the database, from the JSON files
// made by tools/hymns/extract_pdf.py (see tools/hymns/README.md).
//
//	go run ./cmd/import-hymns data/hymns/*.json             // create or update
//	go run ./cmd/import-hymns -dry-run data/hymns/*.json    // only check the files
//	go run ./cmd/import-hymns -prune data/hymns/x.json      // also delete hymns removed from the file
//
// Importing the same file again changes nothing: the book keeps its version
// and the apps don't download it again.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/boriskamtou96/cmfi-connect-backend/internal/db"
	dbs "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/hymns"
	"github.com/boriskamtou96/cmfi-connect-backend/internal/utils"
)

func main() {
	prune := flag.Bool("prune", false, "delete the hymns of the book that are no longer in the file")
	dryRun := flag.Bool("dry-run", false, "check the files without writing to the database")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: import-hymns [-dry-run] [-prune] book.json...")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	// check every file before touching the database
	files := make([]hymns.File, 0, flag.NArg())
	for _, path := range flag.Args() {
		file, err := hymns.Load(path)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: %s (%s), %d hymns\n", path, file.Book.Title, file.Book.Code, len(file.Hymns))
		files = append(files, file)
	}
	if *dryRun {
		fmt.Println("dry run: nothing written")
		return
	}

	cfg, err := utils.LoadConfig()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	conn := db.New(cfg.DB)
	defer conn.Close()
	store := dbs.NewSQLStore(conn)

	for _, file := range files {
		result, err := store.ImportHymnBookTx(context.Background(), file, *prune)
		if err != nil {
			log.Fatalf("%s: %v", file.Book.Code, err)
		}
		status := "unchanged"
		if result.Changed() {
			status = "new version " + result.Book.UpdatedAt.UTC().Format("2006-01-02 15:04:05")
		}
		fmt.Printf("%s: %d new, %d updated, %d unchanged, %d deleted (%s)\n",
			file.Book.Code, result.Inserted, result.Updated, result.Unchanged, result.Deleted, status)
	}
}
