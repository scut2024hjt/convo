package feedcursor

import (
	"encoding/base64"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestRoundtripSameSecondLargeIDs(t *testing.T) {
	stamp := time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)
	for _, id := range []string{"9007199254740993", "9223372036854775807"} {
		encoded := Encode(stamp, id)
		actual, parsed, err := Decode(encoded)
		if err != nil || !actual.Equal(stamp) || parsed != mustParse(t, id) {
			t.Fatalf("roundtrip failed: %s -> %v %v %v", id, actual, parsed, err)
		}
	}
}

func mustParse(t *testing.T, s string) int64 {
	t.Helper()
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestStrictValidation(t *testing.T) {
	invalid := []string{"!", encode(`{}`), encode(`{"t":123,"id":"0"}`),
		encode(`{"t":123,"id":"+1"}`), encode(`{"t":123,"id":"01"}`),
		encode(`{"t":123,"id":9007199254740993}`),
		encode(`{"t":123,"id":"9223372036854775808"}`),
		encode(`{"t":123,"id":"1","unexpected":5}`),
		encode(`{"t":123,"id":"1"} {"t":123,"id":"2"}`),
		encode(`{"t":0,"id":"1"}`)}
	for _, input := range invalid {
		if _, _, err := Decode(input); !errors.Is(err, ErrInvalid) {
			t.Errorf("accepted invalid cursor %q: %v", input, err)
		}
	}
	stamp, id, err := Decode("")
	if stamp != nil || id != 0 || err != nil {
		t.Errorf("empty cursor should be first page")
	}
}

func encode(v string) string { return base64.RawURLEncoding.EncodeToString([]byte(v)) }
