// Package feedcursor serializes MySQL TIMESTAMP(0), post_id keysets without
// losing the 64-bit post ID in JavaScript or changing server timezones.
package feedcursor

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"
)

var ErrInvalid = errors.New("invalid feed cursor")

type keyset struct {
	Timestamp int64  `json:"t"`
	PostID    string `json:"id"`
}

func Encode(t time.Time, id string) string {
	value, _ := json.Marshal(keyset{Timestamp: t.Unix(), PostID: id})
	return base64.RawURLEncoding.EncodeToString(value)
}

func Decode(encoded string) (*time.Time, int64, error) {
	if encoded == "" {
		return nil, 0, nil
	}
	if len(encoded) > 256 {
		return nil, 0, ErrInvalid
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, 0, ErrInvalid
	}
	var value keyset
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&value); err != nil {
		return nil, 0, ErrInvalid
	}
	var extra interface{}
	if err = decoder.Decode(&extra); err != io.EOF {
		return nil, 0, ErrInvalid
	}
	id, err := strconv.ParseInt(value.PostID, 10, 64)
	if err != nil || id <= 0 || value.PostID != strconv.FormatInt(id, 10) || value.Timestamp <= 0 || value.Timestamp > 253402300799 {
		return nil, 0, ErrInvalid
	}
	instant := time.Unix(value.Timestamp, 0).UTC()
	return &instant, id, nil
}
