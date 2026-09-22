package jwt

import (
	"testing"

	"github.com/scut2024hjt/convo/settings"
)

func TestGenerateAndParseToken(t *testing.T) {
	original := settings.Conf.AuthConfig
	settings.Conf.AuthConfig = &settings.AuthConfig{JwtExpire: 1, JwtSecret: "test-secret"}
	t.Cleanup(func() { settings.Conf.AuthConfig = original })

	token, _, err := GenToken(123, "alice", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "123" || claims.Username != "alice" || claims.SessionID != "session-1" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

func TestRejectTokenSignedWithDifferentSecret(t *testing.T) {
	original := settings.Conf.AuthConfig
	settings.Conf.AuthConfig = &settings.AuthConfig{JwtExpire: 1, JwtSecret: "first-secret"}
	t.Cleanup(func() { settings.Conf.AuthConfig = original })

	token, _, err := GenToken(123, "alice", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	settings.Conf.JwtSecret = "second-secret"
	if _, err = ParseToken(token); err == nil {
		t.Fatal("token signed with another secret was accepted")
	}
}

func TestRejectExpiredToken(t *testing.T) {
	original := settings.Conf.AuthConfig
	settings.Conf.AuthConfig = &settings.AuthConfig{JwtExpire: -1, JwtSecret: "test-secret"}
	t.Cleanup(func() { settings.Conf.AuthConfig = original })

	token, _, err := GenToken(123, "alice", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseToken(token); err == nil {
		t.Fatal("expired token was accepted")
	}
}
