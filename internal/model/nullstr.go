package model

import (
	"database/sql/driver"
	"fmt"
)

// NullStr is a string that round-trips Mongo's field-absence semantics
// through Postgres: "" persists as SQL NULL (never the empty-string
// literal) and SQL NULL reads back as "". It is a plain `string` underneath
// with no custom JSON methods, so encoding/json marshals/unmarshals it
// exactly like a plain string — including `,omitempty` (the omitempty check
// is a reflect.Kind == String zero-value check, which NullStr satisfies
// identically to string).
type NullStr string

// Value implements driver.Valuer.
func (s NullStr) Value() (driver.Value, error) {
	if s == "" {
		return nil, nil
	}
	return string(s), nil
}

// Scan implements sql.Scanner.
func (s *NullStr) Scan(value any) error {
	if value == nil {
		*s = ""
		return nil
	}
	switch v := value.(type) {
	case string:
		*s = NullStr(v)
	case []byte:
		*s = NullStr(v)
	default:
		return fmt.Errorf("model.NullStr: cannot scan %T", value)
	}
	return nil
}
