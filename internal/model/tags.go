package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Tag limits (docs/DESIGN_V10_TAGS.md decision #3).
const (
	MaxTagLen  = 30 // runes
	MaxTagsPer = 20
)

// Tags is a task's normalised tag list, stored as a jsonb array. nil and
// empty both persist as '[]' and serialise as [] — never null.
type Tags []string

// Value implements driver.Valuer for the tasks.tags jsonb column.
func (t Tags) Value() (driver.Value, error) {
	b, err := json.Marshal(t.nonNil())
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan implements sql.Scanner for the tasks.tags jsonb column.
func (t *Tags) Scan(src any) error {
	var b []byte
	switch v := src.(type) {
	case nil:
		*t = nil
		return nil
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("model.Tags: cannot scan %T", src)
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("model.Tags: %w", err)
	}
	*t = out
	return nil
}

// MarshalJSON renders nil as [] so `tags` is always an array on the wire.
func (t Tags) MarshalJSON() ([]byte, error) {
	return json.Marshal([]string(t.nonNil()))
}

func (t Tags) nonNil() Tags {
	if t == nil {
		return Tags{}
	}
	return t
}

// NormalizeTags trims, lowercases, drops empties, rejects malformed tags
// (whitespace, ',' or '#' inside, longer than MaxTagLen runes), dedupes
// (first occurrence wins) and rejects more than MaxTagsPer. Pure. The
// result is never nil.
func NormalizeTags(in []string) (Tags, error) {
	out := make(Tags, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, raw := range in {
		tag := strings.ToLower(strings.TrimSpace(raw))
		if tag == "" {
			continue
		}
		if strings.ContainsAny(tag, ",#") || strings.IndexFunc(tag, unicode.IsSpace) >= 0 {
			return nil, fmt.Errorf("invalid tag %q", tag)
		}
		if utf8.RuneCountInString(tag) > MaxTagLen {
			return nil, fmt.Errorf("tag %q is longer than %d characters", tag, MaxTagLen)
		}
		if seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	if len(out) > MaxTagsPer {
		return nil, fmt.Errorf("too many tags (max %d)", MaxTagsPer)
	}
	return out, nil
}

// TagCount is one row of GET /api/tags: a tag and how many visible open
// tasks carry it.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}
