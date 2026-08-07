package hubclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientOverviewReadsTheAgentHubCollections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		if got := r.URL.Query().Get("limit"); got != "200" {
			t.Errorf("limit = %q, want 200", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/fleets":
			_, _ = w.Write([]byte(`{"items":[{"id":"fleet-1","kind":"fleet","label":"Ship API","status":"in-progress"}],"count":1,"limit":200}`))
		case "/api/v1/runs":
			_, _ = w.Write([]byte(`{"items":[{"id":"review-1","kind":"run","type":"review","label":"Review branch","status":"done"}],"count":1,"limit":200}`))
		case "/api/v1/schedules":
			_, _ = w.Write([]byte(`{"items":[{"id":"schedule-1","kind":"schedule","label":"Daily digest","status":"todo"}],"count":1,"limit":200}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(server.URL+"/api/v1", "secret", server.Client())
	require.NoError(t, err)

	items, err := client.Overview(context.Background())

	require.NoError(t, err)
	require.Equal(t, []WorkItem{
		{ID: "fleet-1", Kind: "fleet", Label: "Ship API", Status: "in-progress"},
		{ID: "review-1", Kind: "review", Label: "Review branch", Status: "done"},
		{ID: "schedule-1", Kind: "schedule", Label: "Daily digest", Status: "todo"},
	}, items)
}

func TestClientOverviewReturnsTheAPIsProblemDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"title":"Dependency unavailable","detail":"a source could not be reached"}`))
	}))
	defer server.Close()

	client, err := New(server.URL+"/api/v1", "", server.Client())
	require.NoError(t, err)

	_, err = client.Overview(context.Background())

	require.ErrorContains(t, err, "a source could not be reached")
	require.ErrorContains(t, err, "503")
}

func TestNewRefusesAnInvalidAPIEndpoint(t *testing.T) {
	_, err := New("127.0.0.1:8973/api/v1", "", http.DefaultClient)

	require.ErrorContains(t, err, "absolute http or https URL")
}

func TestNewRefusesPlaintextRemoteEndpoints(t *testing.T) {
	_, err := New("http://hub.example.test/api/v1", "secret", http.DefaultClient)

	require.ErrorContains(t, err, "HTTPS")
}
