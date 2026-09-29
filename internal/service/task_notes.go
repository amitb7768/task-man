package service

// Task domain, part 3: daily notes (the task activity log's note surface).
// Mongo did these as atomic $push / positional $set / $pull on the embedded
// array; here each is a row INSERT/UPDATE/DELETE on task_activity plus an
// updated_at bump on the task row, in one transaction — never a rewrite of
// the whole set.

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"taskman/internal/model"
)

// maxNoteLen bounds a note's text (docs/DESIGN_V9_NOTES_SUMMARY.md).
const maxNoteLen = 4000

// noteTask loads the task a note endpoint targets (full doc, activity
// included — findNote needs it) and enforces canAccessTask. Also returns the
// caller (nil in direct repo tests).
func (s *Service) noteTask(ctx context.Context, taskID string) (*model.Task, *model.CtxUser, error) {
	t, err := s.getTaskRaw(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	u := model.UserFromContext(ctx)
	if u != nil && !canAccessTask(u, t) {
		return nil, nil, forbiddenErr("not allowed")
	}
	return t, u, nil
}

// validateNote validates a note body, returning the trimmed text and the
// resolved date. fallbackDate covers an omitted "date": today-local on add,
// the entry's existing date on edit.
func validateNote(in model.NoteInput, fallbackDate string) (text, date string, err error) {
	text = strings.TrimSpace(in.Text)
	if text == "" {
		return "", "", badRequest("text is required")
	}
	if utf8.RuneCountInString(text) > maxNoteLen {
		return "", "", badRequest("text must be at most %d characters", maxNoteLen)
	}
	if in.Date == "" {
		return text, fallbackDate, nil
	}
	if _, err := model.ParseDate(in.Date); err != nil {
		return "", "", badRequest("invalid date: %s", err.Error())
	}
	return text, in.Date, nil
}

// findNote locates an entry by id: 404 if absent, 400 if it is a status
// entry, 403 if it isn't the caller's (ADMIN exempt).
func findNote(t *model.Task, u *model.CtxUser, noteID string) (*model.ActivityEntry, error) {
	for i := range t.Activity {
		e := &t.Activity[i]
		if e.ID != noteID {
			continue
		}
		if e.Kind != model.ActivityNote {
			return nil, badRequest("only note entries can be edited or deleted")
		}
		if u != nil && u.SystemRole != model.RoleAdmin && (e.By == nil || *e.By != u.ID) {
			return nil, forbiddenErr("not your note")
		}
		return e, nil
	}
	return nil, notFoundErr("note not found")
}

// touchTask bumps updated_at; reports false if the task row is gone. Taking
// the row lock here also serializes a note write against an in-flight
// PatchTask (which holds FOR UPDATE), so neither can lose the other's work.
func touchTask(tx *gorm.DB, taskID string, at time.Time) (bool, error) {
	res := tx.Model(&model.Task{}).Where("id = ?", taskID).Update("updated_at", at)
	return res.RowsAffected > 0, res.Error
}

// AddNote appends a note to a task's timeline (a row INSERT — concurrent
// notes can't clobber each other) and bumps updatedAt.
func (s *Service) AddNote(ctx context.Context, taskID string, in model.NoteInput) (*model.ActivityEntry, error) {
	_, u, err := s.noteTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	now := dbNow()
	text, date, err := validateNote(in, localDate(now))
	if err != nil {
		return nil, err
	}
	e := model.ActivityEntry{
		ID:     NewID(),
		TaskID: taskID,
		Kind:   model.ActivityNote,
		Date:   date,
		At:     now,
		Text:   text,
	}
	if u != nil {
		uid := u.ID
		e.By, e.ByName = &uid, u.Name
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ok, err := touchTask(tx, taskID, now)
		if err != nil {
			return err
		}
		// The task can be deleted between the permission read and the write.
		if !ok {
			return notFoundErr("task not found")
		}
		return tx.Create(&e).Error
	})
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// EditNote rewrites a note's text/date in place, stamping editedAt.
func (s *Service) EditNote(ctx context.Context, taskID, noteID string, in model.NoteInput) (*model.ActivityEntry, error) {
	t, u, err := s.noteTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	e, err := findNote(t, u, noteID)
	if err != nil {
		return nil, err
	}
	now := dbNow()
	text, date, err := validateNote(in, e.Date)
	if err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := touchTask(tx, taskID, now); err != nil {
			return err
		}
		res := tx.Model(&model.ActivityEntry{}).
			Where("id = ? AND task_id = ?", noteID, taskID).
			Updates(map[string]any{"text": text, "date": date, "edited_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return notFoundErr("note not found")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := *e
	out.Text, out.Date, out.EditedAt = text, date, &now
	return &out, nil
}

// DeleteNote removes a note entry. Undo is a client-side re-POST, which lands
// under a fresh id — accepted. Like the Mongo $pull, a note that vanished
// between read and write is not an error.
func (s *Service) DeleteNote(ctx context.Context, taskID, noteID string) error {
	t, u, err := s.noteTask(ctx, taskID)
	if err != nil {
		return err
	}
	if _, err := findNote(t, u, noteID); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := touchTask(tx, taskID, time.Now()); err != nil {
			return err
		}
		return tx.Where("id = ? AND task_id = ?", noteID, taskID).Delete(&model.ActivityEntry{}).Error
	})
}
