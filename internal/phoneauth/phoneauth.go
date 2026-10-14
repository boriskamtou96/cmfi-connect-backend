// Package phoneauth checks that a person holds a phone number: the app
// verifies it with an SMS code through Firebase Authentication, then sends
// the ID token Firebase gave it.
package phoneauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

// MaxAge is how long after the SMS code a token still proves the number.
const MaxAge = 10 * time.Minute

var (
	// ErrUnavailable: no Firebase key in the configuration.
	ErrUnavailable = errors.New("phone verification is not configured")
	// ErrInvalid: a token Firebase didn't sign, expired, too old, or without
	// a phone number.
	ErrInvalid = errors.New("invalid or expired phone verification")
)

// Verifier gives the number (E.164, "+237690000001") a token proves.
type Verifier interface {
	VerifiedPhone(ctx context.Context, idToken string) (string, error)
}

// Disabled proves nothing: no Firebase key in the configuration.
type Disabled struct{}

func (Disabled) VerifiedPhone(context.Context, string) (string, error) {
	return "", ErrUnavailable
}

// Firebase checks the tokens of Firebase Authentication.
type Firebase struct {
	client *auth.Client
}

// NewFirebase reads the key of a Firebase service account (the same JSON
// file as the push).
func NewFirebase(ctx context.Context, credentialsFile string) (*Firebase, error) {
	app, err := firebase.NewApp(ctx, nil, option.WithAuthCredentialsFile(option.ServiceAccount, credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("firebase.NewApp: %w", err)
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("firebase auth: %w", err)
	}
	return &Firebase{client: client}, nil
}

func (f *Firebase) VerifiedPhone(ctx context.Context, idToken string) (string, error) {
	token, err := f.client.VerifyIDToken(ctx, idToken)
	if err != nil {
		return "", ErrInvalid
	}
	phone, _ := token.Claims["phone_number"].(string)
	if phone == "" || time.Since(time.Unix(token.AuthTime, 0)) > MaxAge {
		return "", ErrInvalid
	}
	return phone, nil
}
