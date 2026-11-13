package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"
)

// ImportShippedGameCourses imports, at start, the courses of [files] the
// database does not have yet (by their code): a new deployment brings its
// new courses without anyone importing them. A course that exists is never
// touched, even when its file changed: the admins may have corrected it in
// the back-office since. A file that can't be read is said, the others still
// go in.
func (s *Server) ImportShippedGameCourses(ctx context.Context, files fs.FS) error {
	names, err := fs.Glob(files, "*.json")
	if err != nil {
		return err
	}
	var failed []error
	for _, name := range names {
		imported, err := s.importShippedCourse(ctx, files, name)
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", name, err))
			continue
		}
		if imported != "" {
			slog.Info("game course imported", "file", name, "code", imported)
		}
	}
	return errors.Join(failed...)
}

// importShippedCourse imports the course of file [name]; its code once
// imported, "" when it was already there.
func (s *Server) importShippedCourse(ctx context.Context, files fs.FS, name string) (string, error) {
	raw, err := fs.ReadFile(files, name)
	if err != nil {
		return "", err
	}
	var course importGameCourseRequest
	if err := json.Unmarshal(raw, &course); err != nil {
		return "", err
	}
	if err := course.validate(); err != nil {
		return "", err
	}
	if _, err := s.store.GetGameCourseByCode(ctx, course.Code); err == nil {
		return "", nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if _, err := s.store.ImportGameCourseTx(ctx, course.toImport(), time.Now()); err != nil {
		return "", err
	}
	return course.Code, nil
}
