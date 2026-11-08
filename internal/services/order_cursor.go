package services

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"backend/internal/domain"
)

// maxOrderCursorLength bounds the opaque cursor before it is decoded; a real one is about 60 characters.
const maxOrderCursorLength = 256

// errInvalidOrderCursor is the message of every cursor decoding failure (400 VALIDATION_ERROR).
const errInvalidOrderCursor = "cursor is invalid"

// orderCursorJSON is the payload behind the opaque cursor: {"c": created_at (RFC3339Nano, UTC), "i": id}.
type orderCursorJSON struct {
	C string `json:"c"`
	I uint   `json:"i"`
}

// encodeOrderCursor turns a keyset position into the opaque, URL-safe cursor of GET /orders
// (base64.RawURLEncoding of orderCursorJSON). Sub-second precision is kept, so microsecond timestamps
// from Postgres round-trip exactly.
func encodeOrderCursor(c domain.OrderCursor) string {
	raw, _ := json.Marshal(orderCursorJSON{C: c.CreatedAt.UTC().Format(time.RFC3339Nano), I: c.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeOrderCursor parses a cursor made by encodeOrderCursor. Anything else (bad base64, bad JSON, a
// missing or zero time, id 0) is a *domain.ValidationError "cursor is invalid".
func decodeOrderCursor(s string) (domain.OrderCursor, error) {
	invalid := &domain.ValidationError{Msg: errInvalidOrderCursor}
	if s == "" || len(s) > maxOrderCursorLength {
		return domain.OrderCursor{}, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return domain.OrderCursor{}, invalid
	}
	var payload orderCursorJSON
	if err := json.Unmarshal(raw, &payload); err != nil {
		return domain.OrderCursor{}, invalid
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.C)
	if err != nil || createdAt.IsZero() || payload.I == 0 {
		return domain.OrderCursor{}, invalid
	}
	return domain.OrderCursor{CreatedAt: createdAt.UTC(), ID: payload.I}, nil
}
