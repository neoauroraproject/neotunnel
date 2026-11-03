// Package webhook posts the daemon's events to a program of the owner's (a
// sales service, a monitor): each event as JSON, signed so the receiver
// knows it came from this server, retried a few times if it fails.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"neotunnel/internal/config"
	"neotunnel/internal/events"
)

// Payload is what each post carries.
type Payload struct {
	// Node is this server's name.
	Node  string       `json:"node"`
	Event events.Event `json:"event"`
	// Test marks the panel's test post.
	Test bool `json:"test,omitempty"`
}

// retries are the waits before each new try of a failed post.
var retries = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}

var client = &http.Client{Timeout: 15 * time.Second}

// Watch posts every event of this process to the webhook the settings
// name, read again for each event.
func Watch() {
	events.OnAdd(func(e events.Event) {
		st, err := config.Load()
		if err != nil || st.Panel == nil || st.Panel.Webhook == nil || st.Panel.Webhook.URL == "" {
			return
		}
		w := *st.Panel.Webhook
		p := Payload{Node: st.Self, Event: e}
		for i := 0; ; i++ {
			err := Post(context.Background(), w, p)
			if err == nil {
				return
			}
			if i == len(retries) {
				log.Printf("webhook: %s event of %s not delivered: %v", e.Kind, e.Tunnel, err)
				return
			}
			time.Sleep(retries[i])
		}
	})
}

// Post sends one payload.
func Post(ctx context.Context, w config.Webhook, p Payload) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "NeoTunnel-Webhook")
	req.Header.Set("X-NeoTunnel-Signature", Sign(w.Secret, body))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s answered %s", w.URL, resp.Status)
	}
	return nil
}

// Sign is the signature header of a body: "sha256=" and the hex HMAC.
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Check validates a webhook's settings.
func Check(w config.Webhook) error {
	if w.URL == "" {
		return nil
	}
	u, err := url.Parse(w.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("the webhook needs an http:// or https:// address")
	}
	if len(w.Secret) < 16 {
		return fmt.Errorf("the webhook's secret must be at least 16 characters")
	}
	return nil
}
