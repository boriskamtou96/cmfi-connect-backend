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

	db "github.com/boriskamtou96/cmfi-connect-backend/internal/db/sqlc"
)

// ImportShippedGameCourses imports, at start, the courses of [files] the
// database does not have yet (by their code): a new deployment brings its
// new courses without anyone importing them. Of a course that exists, only
// the levels added to its file since are added, after its own (14/11/2026:
// new questions reach the members without an import, which would erase
// what they played); what exists is never changed, the admins may have
// corrected it in the back-office. A file that can't be read is said, the
// others still go in.
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
	if existing, err := s.store.GetGameCourseByCode(ctx, course.Code); err == nil {
		// the course is there: only the levels added to the file since
		return "", s.appendShippedLevels(ctx, existing, course, name)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if _, err := s.store.ImportGameCourseTx(ctx, course.toImport(), time.Now()); err != nil {
		return "", err
	}
	return course.Code, nil
}

// appendShippedLevels adds to [existing] the levels of [course] it lacks (see
// AppendGameLevelsTx), published like the course in its file.
func (s *Server) appendShippedLevels(ctx context.Context, existing db.GetGameCourseByCodeRow, course importGameCourseRequest, name string) error {
	published := sql.NullTime{Time: time.Now(), Valid: course.Published}
	added, err := s.store.AppendGameLevelsTx(ctx, existing.ID, course.toImport().Levels, published)
	if err != nil {
		return err
	}
	if added.Levels > 0 {
		slog.Info("game levels added", "file", name, "code", course.Code,
			"levels", added.Levels, "lessons", added.Lessons, "questions", added.Questions)
	}
	return nil
}
