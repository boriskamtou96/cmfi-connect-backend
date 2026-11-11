// Package socialauth checks who signs in with Google or Apple: the app signs
// in with Firebase Authentication, then sends the ID token Firebase gave it.
package socialauth

import (
	"context"
	"errors"
	"fmt"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

// The providers a member signs in with.
const (
	Google = "google"
	Apple  = "apple"
)

var (
	// ErrUnavailable: no Firebase key in the configuration.
	ErrUnavailable = errors.New("sign-in with Google or Apple is not configured")
	// ErrInvalid: a token Firebase didn't sign, expired, or of another
	// provider (a phone, an email).
	ErrInvalid = errors.New("invalid or expired Google or Apple sign-in")
)

// Identity is the Google or Apple account a token proves.
type Identity struct {
	// Google or Apple
	Provider string
	// the Firebase user: the same each time this account signs in
	Subject string
	Email   string
	// the provider checked the email (Apple's private relay is checked too)
	EmailVerified bool
	// the name the account gives ("" for Apple after the first sign-in)
	Name string
}

// Verifier gives the identity a token proves.
type Verifier interface {
	Identity(ctx context.Context, idToken string) (Identity, error)
}

// Disabled proves nothing: no Firebase key in the configuration.
type Disabled struct{}

func (Disabled) Identity(context.Context, string) (Identity, error) {
	return Identity{}, ErrUnavailable
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

// providers: the sign-in providers of Firebase this server accepts.
var providers = map[string]string{
	"google.com": Google,
	"apple.com":  Apple,
}

func (f *Firebase) Identity(ctx context.Context, idToken string) (Identity, error) {
	token, err := f.client.VerifyIDToken(ctx, idToken)
	if err != nil {
		return Identity{}, ErrInvalid
	}
	provider, ok := providers[token.Firebase.SignInProvider]
	if !ok || token.UID == "" {
		return Identity{}, ErrInvalid
	}
	email, _ := token.Claims["email"].(string)
	verified, _ := token.Claims["email_verified"].(bool)
	name, _ := token.Claims["name"].(string)
	return Identity{
		Provider:      provider,
		Subject:       token.UID,
		Email:         email,
		EmailVerified: verified,
		Name:          name,
	}, nil
}
