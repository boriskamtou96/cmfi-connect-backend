package token

import "time"

type JWTAuthenticator interface {
	GenerateToken(userID int64, phoneNumber string, duration time.Duration) (string, error)
	VerifyToken(token string) (*Payload, error)
}
