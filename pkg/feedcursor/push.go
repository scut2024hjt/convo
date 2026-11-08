package feedcursor

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strconv"
	"time"
)

// Push cursors intentionally have a different schema from legacy pull cursors.
// The recipient is always checked against the authenticated JWT identity.
type pushKeyset struct {
	Version   int    `json:"v"`
	Mode      string `json:"m"`
	Recipient string `json:"u"`
	Timestamp int64  `json:"t"`
	PostID    string `json:"id"`
}

func EncodePush(t time.Time, id string, recipient int64) string {
	b, _ := json.Marshal(pushKeyset{Version: 1, Mode: "push", Recipient: strconv.FormatInt(recipient, 10), Timestamp: t.Unix(), PostID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodePush(encoded string, recipient int64) (*time.Time, int64, error) {
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
	var value pushKeyset
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil {
		return nil, 0, ErrInvalid
	}
	var extra interface{}
	if decoder.Decode(&extra) != io.EOF {
		return nil, 0, ErrInvalid
	}
	if value.Version != 1 || value.Mode != "push" || value.Recipient != strconv.FormatInt(recipient, 10) {
		return nil, 0, ErrInvalid
	}
	id, err := strconv.ParseInt(value.PostID, 10, 64)
	if err != nil || id <= 0 || value.PostID != strconv.FormatInt(id, 10) || value.Timestamp <= 0 || value.Timestamp > 253402300799 {
		return nil, 0, ErrInvalid
	}
	t := time.Unix(value.Timestamp, 0).UTC()
	return &t, id, nil
}
