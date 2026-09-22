package jwt

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/scut2024hjt/convo/settings"
)

//const TokenExpireDuration = time.Hour * 2

type MyClaims struct {
	Username  string `json:"username"`
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

func ExpireDuration() time.Duration {
	return time.Duration(settings.Conf.JwtExpire) * time.Hour
}

func GenToken(userID int64, username, sessionID string) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(ExpireDuration()).Truncate(time.Second)
	c := MyClaims{
		Username:  username,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10),
			Issuer:    "convo",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	tokenString, err := token.SignedString([]byte(settings.Conf.JwtSecret))
	return tokenString, expiresAt, err
}

func ParseToken(tokenString string) (*MyClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &MyClaims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
		}
		return []byte(settings.Conf.JwtSecret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer("convo"))
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*MyClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}
