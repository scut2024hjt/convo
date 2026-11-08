package feedcursor

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestPushCursorRoundTripAndScope(t *testing.T) {
	wantTime := time.Unix(1800000000, 0).UTC()
	encoded := EncodePush(wantTime, "9007199254740993", 98)
	gotTime, gotID, err := DecodePush(encoded, 98)
	if err != nil || gotTime.Unix() != wantTime.Unix() || gotID != 9007199254740993 {
		t.Fatalf("round trip: %v %v %v", gotTime, gotID, err)
	}
	if _, _, err = DecodePush(encoded, 97); err != ErrInvalid {
		t.Fatalf("cross-user cursor should fail: %v", err)
	}
	if _, _, err = Decode(encoded); err != ErrInvalid {
		t.Fatalf("cross-mode cursor should fail: %v", err)
	}
	old := Encode(wantTime, "9007199254740993")
	if _, _, err = DecodePush(old, 98); err != ErrInvalid {
		t.Fatalf("pull cursor must fail in push: %v", err)
	}
	tampered := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"m":"push","u":"98","t":1800000000,"id":"123","x":2}`))
	if _, _, err = DecodePush(tampered, 98); err != ErrInvalid {
		t.Fatalf("unexpected keys must fail: %v", err)
	}
}
