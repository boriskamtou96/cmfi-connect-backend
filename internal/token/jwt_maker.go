package token

import (
	"fmt"
	"time"

	"github.com/o1egl/paseto"
	"golang.org/x/crypto/chacha20poly1305"
)

type PasetoMaker struct {
	paseto      paseto.V2
	symetricKey []byte
}

func NewPasetoMaker(symetricKey string) (JWTAuthenticator, error) {
	if len(symetricKey) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("invalid key size")
	}

	maker := &PasetoMaker{
		paseto:      paseto.V2{},
		symetricKey: []byte(symetricKey),
	}
	return maker, nil
}

func (p *PasetoMaker) GenerateToken(username string, duration time.Duration) (string, error) {
	payload, err := NewPayload(username, duration)
	if err != nil {
		return "", err
	}
	return p.paseto.Encrypt(p.symetricKey, payload, nil)
}

func (p *PasetoMaker) VerifyToken(token string) (*Payload, error) {
	var payload Payload
	err := p.paseto.Decrypt(token, p.symetricKey, &payload, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	err = payload.Valid()
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	return &payload, nil
}
