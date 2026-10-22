// Package revenuecat gives Premium to a member who paid outside the stores
// (mobile money on the website): a promotional entitlement in RevenueCat
// (REST API v1), seen by the app like a purchase.
package revenuecat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const baseURL = "https://api.revenuecat.com/v1"

// Client with a secret key (sk_…, kept on the server only).
type Client struct {
	BaseURL     string
	SecretKey   string
	Entitlement string
	HTTP        *http.Client
}

func New(secretKey, entitlement string) *Client {
	return &Client{
		BaseURL:     baseURL,
		SecretKey:   secretKey,
		Entitlement: entitlement,
		HTTP:        &http.Client{Timeout: 20 * time.Second},
	}
}

// Grant gives the entitlement to [appUserID] (the id the app logs in with)
// until [until]; for life when nil.
func (c *Client) Grant(ctx context.Context, appUserID string, until *time.Time) error {
	body := map[string]any{"duration": "lifetime"}
	if until != nil {
		body = map[string]any{"end_time_ms": until.UnixMilli()}
	}
	return c.post(ctx, appUserID, "promotional", body)
}

// Revoke takes back the entitlements given by [Grant] (a refund).
func (c *Client) Revoke(ctx context.Context, appUserID string) error {
	return c.post(ctx, appUserID, "revoke_promotionals", map[string]any{})
}

func (c *Client) post(ctx context.Context, appUserID, action string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	path := fmt.Sprintf("%s/subscribers/%s/entitlements/%s/%s", strings.TrimSuffix(c.BaseURL, "/"),
		url.PathEscape(appUserID), url.PathEscape(c.Entitlement), action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("revenuecat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("revenuecat %s: %d %s", action, resp.StatusCode, data)
	}
	return nil
}
