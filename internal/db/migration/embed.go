// Package migration holds the SQL migrations, embedded in the binary so the
// server can apply the pending ones itself when it starts (Render runs the
// binary alone: there is no migrate tool next to it).
package migration

import "embed"

// Files are the *.up.sql and *.down.sql migrations, numbered in order.
//
//go:embed *.sql
var Files embed.FS
