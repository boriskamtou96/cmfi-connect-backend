package token

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Payload struct {
	ID          uuid.UUID `json:"id"`
	PhoneNumber string    `json:"username"`
	IssueAt     time.Time `json:"issue_at"`
	ExpireAt    time.Time `json:"expire_at"`
}

func NewPayload(username string, duration time.Duration) (*Payload, error) {
	tokenID, err := uuid.NewUUID()
	if err != nil {
		return nil, err
	}

	payload := &Payload{
		ID:          tokenID,
		PhoneNumber: username,
		IssueAt:     time.Now(),
		ExpireAt:    time.Now().Add(duration),
	}

	return payload, nil
}

func (p *Payload) Valid() error {
	if time.Now().After(p.ExpireAt) {
		return fmt.Errorf("token has expired")
	}
	return nil
}
