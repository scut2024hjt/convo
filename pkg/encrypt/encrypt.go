package encrypt

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

var ErrPasswordTooLong = errors.New("password exceeds bcrypt 72-byte limit")

func EncryptPassword(password string) (string, error) {
	if len([]byte(password)) > 72 {
		return "", ErrPasswordTooLong
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func ComparePassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
