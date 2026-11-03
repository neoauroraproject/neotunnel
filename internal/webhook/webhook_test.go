package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"neotunnel/internal/config"
	"neotunnel/internal/events"
)

// A post carries the event, signed with the secret; a receiver that
// refuses it is an error.
func TestPost(t *testing.T) {
	var got Payload
	var sig string
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sig = r.Header.Get("X-NeoTunnel-Signature")
		if sig != Sign("0123456789abcdef", b) {
			w.WriteHeader(401)
			return
		}
		json.Unmarshal(b, &got)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	w := config.Webhook{URL: srv.URL, Secret: "0123456789abcdef"}
	p := Payload{Node: "ir", Event: events.Event{Tunnel: "t1", Kind: events.Port, To: "quota", Detail: "443"}}
	if err := Post(context.Background(), w, p); err != nil {
		t.Fatal(err)
	}
	if got.Node != "ir" || got.Event.Kind != "port" || got.Event.To != "quota" || got.Event.Detail != "443" {
		t.Errorf("received %+v", got)
	}
	if err := Post(context.Background(), config.Webhook{URL: srv.URL, Secret: "another-secret-123"}, p); err == nil {
		t.Error("a wrong signature was taken")
	}
	status = 500
	if err := Post(context.Background(), w, p); err == nil {
		t.Error("a 500 is no delivery")
	}
}

func TestCheck(t *testing.T) {
	for _, c := range []struct {
		w  config.Webhook
		ok bool
	}{
		{config.Webhook{}, true},
		{config.Webhook{URL: "https://example.com/hook", Secret: "0123456789abcdef"}, true},
		{config.Webhook{URL: "https://example.com/hook", Secret: "short"}, false},
		{config.Webhook{URL: "ftp://example.com", Secret: "0123456789abcdef"}, false},
		{config.Webhook{URL: "example.com", Secret: "0123456789abcdef"}, false},
	} {
		if err := Check(c.w); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c.w, err)
		}
	}
}
