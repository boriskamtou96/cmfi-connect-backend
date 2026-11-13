// Package games holds the courses shipped with the server, one JSON file per
// course (the format of POST /v1/admin/games/import). They are built into
// the binary: the image of production has no data folder.
package games

import "embed"

// Courses are the files *.json of this folder.
//
//go:embed *.json
var Courses embed.FS
