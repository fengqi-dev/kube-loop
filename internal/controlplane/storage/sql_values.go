package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func parseTime(raw string, field string) (time.Time, error) {
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, errors.New("decode " + field)
	}
	return value, nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func parseNullableTime(raw sql.NullString, field string) (*time.Time, error) {
	if !raw.Valid {
		return nil, nil //nolint:nilnil // SQL NULL is represented by a nil optional timestamp.
	}
	value, err := parseTime(raw.String, field)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func normalizeJSON(
	value json.RawMessage,
	required bool,
	field string,
) (json.RawMessage, error) {
	if len(value) == 0 {
		if required {
			return nil, errors.New(field + " is required")
		}
		return nil, nil
	}
	if !json.Valid(value) {
		return nil, errors.New(field + " must contain valid JSON")
	}
	return append(json.RawMessage(nil), value...), nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}
