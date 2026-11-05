package logic

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFeedCursorRoundtripLargeIDAndSameTimestamp(t *testing.T) {
	stamp := time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)
	const id = "9007199254740993"
	encoded := encodeFeedCursor(stamp, id)
	gotTime, gotID, err := decodeFeedCursor(encoded)
	require.NoError(t, err)
	require.Equal(t, stamp, *gotTime)
	require.Equal(t, int64(9007199254740993), gotID)
	// A same-second next page must use post_id to avoid duplicates/omissions.
	require.Equal(t, stamp.Unix(), gotTime.Unix())
}

func TestFeedCursorRejectsInvalidInputs(t *testing.T) {
	invalid := []string{
		"!not-base64!", base64.RawURLEncoding.EncodeToString([]byte(`{}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"t":123,"id":"-2"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"t":123,"id":9007199254740993}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"t":123,"id":"9223372036854775808"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"t":0,"id":"1"}`)),
	}
	for _, cursor := range invalid {
		_, _, err := decodeFeedCursor(cursor)
		require.ErrorIs(t, err, ErrInvalidFeedCursor)
	}
}
