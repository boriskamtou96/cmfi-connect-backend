package token

import "time"

type JWTAuthenticator interface {
	GenerateToken(phoneNumber string, duration time.Duration) (string, error)
	VerifyToken(token string) (*Payload, error)
}
