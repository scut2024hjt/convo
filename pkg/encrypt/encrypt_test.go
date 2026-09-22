package encrypt

import "testing"

func TestPasswordHashAndCompare(t *testing.T) {
	first, err := EncryptPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncryptPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("bcrypt hashes should contain independent salts")
	}
	if err = ComparePassword(first, "correct horse battery staple"); err != nil {
		t.Fatalf("correct password was rejected: %v", err)
	}
	if err = ComparePassword(first, "wrong password"); err == nil {
		t.Fatal("wrong password was accepted")
	}
}

func TestPasswordLengthLimit(t *testing.T) {
	password := make([]byte, 73)
	if _, err := EncryptPassword(string(password)); err != ErrPasswordTooLong {
		t.Fatalf("expected ErrPasswordTooLong, got %v", err)
	}
}
