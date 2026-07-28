package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"temporal-agents/internal/notification"
)

func TestWebhookPostsNotificationAsJSON(t *testing.T) {
	var gotMethod, gotContentType string
	var gotBody webhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n := notification.Notification{Title: "chain done", Body: "3 commits", URL: "https://github.com/acme/widgets/pull/7"}
	if err := NewWebhook(srv.URL).Notify(context.Background(), n); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
	if gotBody.Title != n.Title || gotBody.Body != n.Body {
		t.Errorf("payload = %+v, want %+v", gotBody, n)
	}
	// The PR hyperlink is carried through to the webhook payload.
	if gotBody.URL != n.URL {
		t.Errorf("payload url = %q, want %q", gotBody.URL, n.URL)
	}
}

func TestWebhookFailsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := NewWebhook(srv.URL).Notify(context.Background(), notification.Notification{}); err == nil {
		t.Fatal("expected an error for a 500 response, got nil")
	}
}
