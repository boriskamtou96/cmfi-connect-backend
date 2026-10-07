package token

import (
	"fmt"
	"time"
)

type Payload struct {
	ID          int64     `json:"id"`
	PhoneNumber string    `json:"username"`
	IssueAt     time.Time `json:"issue_at"`
	ExpireAt    time.Time `json:"expire_at"`
}

func NewPayload(userID int64, phoneNumber string, duration time.Duration) (*Payload, error) {

	payload := &Payload{
		ID:          userID,
		PhoneNumber: phoneNumber,
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
