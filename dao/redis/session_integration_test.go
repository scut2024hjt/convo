//go:build integration
// +build integration

package redis

import (
	"strconv"
	"testing"
	"time"

	"github.com/scut2024hjt/convo/settings"
)

func TestSessionReplacementAndCompareDelete(t *testing.T) {
	if err := Init(&settings.RedisConfig{Host: "127.0.0.1", Port: 36379, PoolSize: 4}); err != nil {
		t.Skipf("redis integration service is unavailable: %v", err)
	}
	defer Close()

	userID := time.Now().UnixNano()
	defer client.Del(getRedisKey(KeyAuthSessionPrefix + strconv.FormatInt(userID, 10)))

	if err := SetSession(userID, "session-a", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := SetSession(userID, "session-b", time.Hour); err != nil {
		t.Fatal(err)
	}
	valid, err := ValidateSession(userID, "session-a")
	if err != nil || valid {
		t.Fatalf("old session remained valid: valid=%v err=%v", valid, err)
	}
	if err = DeleteSession(userID, "session-a"); err != nil {
		t.Fatal(err)
	}
	valid, err = ValidateSession(userID, "session-b")
	if err != nil || !valid {
		t.Fatalf("compare-delete removed new session: valid=%v err=%v", valid, err)
	}
	if err = DeleteSession(userID, "session-b"); err != nil {
		t.Fatal(err)
	}
	valid, err = ValidateSession(userID, "session-b")
	if err != nil || valid {
		t.Fatalf("logout did not invalidate session: valid=%v err=%v", valid, err)
	}
}
