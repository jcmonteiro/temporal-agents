package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"temporal-agents/internal/codereview"
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

	n := codereview.Notification{Title: "chain done", Body: "3 commits"}
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
}

func TestWebhookFailsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := NewWebhook(srv.URL).Notify(context.Background(), codereview.Notification{}); err == nil {
		t.Fatal("expected an error for a 500 response, got nil")
	}
}
