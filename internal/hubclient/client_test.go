package hubclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/workoverview"
)

func TestClientOverviewReadsEveryActiveWorkPage(t *testing.T) {
	var fleetPages atomic.Int32
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
			if got := r.URL.Query().Get("active"); got != "true" {
				t.Errorf("active = %q, want true", got)
			}
			if r.URL.Query().Get("cursor") == "" {
				fleetPages.Add(1)
				_, _ = fmt.Fprint(w, `{"items":[{"id":"fleet-1","kind":"fleet","label":"Ship API","status":"todo","running":true}],"count":1,"limit":200,"next":"/api/v1/fleets?active=true&cursor=page-2&limit=200"}`)
				return
			}
			fleetPages.Add(1)
			_, _ = fmt.Fprint(w, `{"items":[{"id":"fleet-2","kind":"fleet","label":"Fix API","status":"failed","running":true}],"count":1,"limit":200,"next":null}`)
		case "/api/v1/runs":
			if got := r.URL.Query().Get("active"); got != "true" {
				t.Errorf("active = %q, want true", got)
			}
			_, _ = fmt.Fprint(w, `{"items":[{"id":"review-1","kind":"run","type":"review","label":"Review branch","status":"in-progress","running":true}],"count":1,"limit":200,"next":null}`)
		case "/api/v1/schedules":
			_, _ = fmt.Fprint(w, `{"items":[{"id":"schedule-1","kind":"schedule","label":"Daily digest","status":"todo"}],"count":1,"limit":200,"next":null}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(server.URL+"/api/v1", "secret", server.Client())
	require.NoError(t, err)

	items, err := client.Overview(context.Background())

	require.NoError(t, err)
	require.Equal(t, int32(2), fleetPages.Load())
	require.Equal(t, []workoverview.Item{
		{ID: "fleet-1", Kind: workoverview.KindFleet, Status: workoverview.StatusTodo, Running: true},
		{ID: "fleet-2", Kind: workoverview.KindFleet, Status: workoverview.StatusFailed, Running: true},
		{ID: "review-1", Kind: workoverview.KindReview, Status: workoverview.StatusInProgress, Running: true},
		{ID: "schedule-1", Kind: workoverview.KindSchedule, Status: workoverview.StatusTodo},
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

func TestClientOverviewRejectsMalformedSuccessfulDocuments(t *testing.T) {
	tests := map[string]string{
		"missing items":     `{"count":0,"limit":200,"next":null}`,
		"wrong count":       `{"items":[],"count":1,"limit":200,"next":null}`,
		"missing next":      `{"items":[],"count":0,"limit":200}`,
		"incomplete item":   `{"items":[{"id":"fleet-1","kind":"fleet"}],"count":1,"limit":200,"next":null}`,
		"unknown item kind": `{"items":[{"id":"fleet-1","kind":"job","status":"todo","running":true}],"count":1,"limit":200,"next":null}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, body)
			}))
			defer server.Close()
			client, err := New(server.URL+"/api/v1", "", server.Client())
			require.NoError(t, err)

			_, err = client.Overview(context.Background())

			require.Error(t, err)
		})
	}
}

func TestClientRejectsRedirectsWithoutForwardingAuthorization(t *testing.T) {
	var redirectedRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("redirect target received Authorization")
		}
	}))
	defer target.Close()

	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer origin.Close()
	client, err := New(origin.URL+"/api/v1", "secret", origin.Client())
	require.NoError(t, err)

	_, err = client.Overview(context.Background())

	require.ErrorContains(t, err, "HTTP 302")
	require.Equal(t, int32(0), redirectedRequests.Load())
}

func TestNewRefusesAnInvalidAPIEndpoint(t *testing.T) {
	_, err := New("127.0.0.1:8973/api/v1", "", http.DefaultClient)

	require.ErrorContains(t, err, "absolute http or https URL")
}

func TestNewRefusesPlaintextRemoteEndpoints(t *testing.T) {
	_, err := New("http://hub.example.test/api/v1", "secret", http.DefaultClient)

	require.ErrorContains(t, err, "HTTPS")
}
