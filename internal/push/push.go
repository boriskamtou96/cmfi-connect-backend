// Package push sends the notifications that show on the members' phones,
// through Firebase Cloud Messaging. Without a Firebase key the server still
// runs: the announcements reach the apps, only the push is not sent.
package push

import (
	"context"
	"fmt"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"
)

// batchSize is the most phones Firebase takes in one call.
const batchSize = 500

// Message is what a phone shows. [ImageURL] is absolute (the phone fetches
// it), empty for none; [Data] tells the app what to open.
type Message struct {
	Title    string
	Body     string
	ImageURL string
	Data     map[string]string
}

// Result of a send: [Sent] phones accepted by Firebase, and the [Dead]
// tokens it no longer knows (app removed, token replaced), to forget.
type Result struct {
	Sent int
	Dead []string
}

type Sender interface {
	Send(ctx context.Context, tokens []string, message Message) (Result, error)
}

// Disabled sends nothing: no Firebase key in the configuration.
type Disabled struct{}

func (Disabled) Send(context.Context, []string, Message) (Result, error) { return Result{}, nil }

// Firebase sends through Firebase Cloud Messaging.
type Firebase struct {
	client *messaging.Client
}

// NewFirebase reads the key of a Firebase service account (the JSON file of
// the project, kept out of git).
func NewFirebase(ctx context.Context, credentialsFile string) (*Firebase, error) {
	app, err := firebase.NewApp(ctx, nil, option.WithAuthCredentialsFile(option.ServiceAccount, credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("firebase.NewApp: %w", err)
	}
	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("firebase messaging: %w", err)
	}
	return &Firebase{client: client}, nil
}

func (f *Firebase) Send(ctx context.Context, tokens []string, message Message) (Result, error) {
	var result Result
	for start := 0; start < len(tokens); start += batchSize {
		batch := tokens[start:min(start+batchSize, len(tokens))]
		response, err := f.client.SendEachForMulticast(ctx, multicast(batch, message))
		if err != nil {
			return result, fmt.Errorf("firebase send: %w", err)
		}
		result.Sent += response.SuccessCount
		for i, sent := range response.Responses {
			if !sent.Success && messaging.IsUnregistered(sent.Error) {
				result.Dead = append(result.Dead, batch[i])
			}
		}
	}
	return result, nil
}

func multicast(tokens []string, message Message) *messaging.MulticastMessage {
	return &messaging.MulticastMessage{
		// the apps give registration tokens (firebase_messaging getToken)
		Tokens: tokens,
		Data:   message.Data,
		Notification: &messaging.Notification{
			Title:    message.Title,
			Body:     message.Body,
			ImageURL: message.ImageURL,
		},
		Android: &messaging.AndroidConfig{
			Priority: "high",
			Notification: &messaging.AndroidNotification{
				// the channel and the white dove of the app (res/drawable)
				ChannelID: "announcements",
				Icon:      "ic_notification",
			},
		},
		APNS: &messaging.APNSConfig{
			Payload: &messaging.APNSPayload{
				Aps: &messaging.Aps{Sound: "default", MutableContent: message.ImageURL != ""},
			},
			FCMOptions: &messaging.APNSFCMOptions{ImageURL: message.ImageURL},
		},
	}
}
