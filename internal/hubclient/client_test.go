package hubclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/agenthub/agenthubtest"
	"temporal-agents/internal/httpapi"
	"temporal-agents/internal/workoverview"
)

func TestClientOverviewReadsEveryActiveWorkPage(t *testing.T) {
	var pages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		if got := r.URL.Query().Get("limit"); got != "200" {
			t.Errorf("limit = %q, want 200", got)
		}
		if r.URL.Path != "/api/v1/active-work" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		pages.Add(1)
		if r.URL.Query().Get("cursor") == "" {
			_, _ = fmt.Fprint(w, `{"items":[{"id":"fleet-1","type":"fleet","status":"in-progress","running":true}],"count":1,"limit":200,"next":"/api/v1/active-work?cursor=page-2&limit=200"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"items":[{"id":"schedule-1","type":"schedule","status":"todo","running":false}],"count":1,"limit":200,"next":null}`)
	}))
	defer server.Close()

	client, err := New(server.URL+"/api/v1", "secret", server.Client())
	require.NoError(t, err)

	items, err := client.Overview(context.Background())

	require.NoError(t, err)
	require.Equal(t, int32(2), pages.Load())
	require.Equal(t, []workoverview.Item{
		{ID: "fleet-1", Kind: workoverview.KindFleet, Status: workoverview.StatusInProgress, Running: true},
		{ID: "schedule-1", Kind: workoverview.KindSchedule, Status: workoverview.StatusTodo},
	}, items)
}

func TestHTTPServerAndClientShareTheActiveWorkContract(t *testing.T) {
	executions := make([]agenthub.Execution, 0, 201)
	want := make([]workoverview.Item, 0, 402)
	for i := 0; i < 201; i++ {
		id := fmt.Sprintf("run-%03d", i)
		executions = append(executions, agenthubtest.Run(
			id, "", agenthub.OutcomeRunning, time.Unix(int64(i), 0).UTC()))
		want = append(want, workoverview.Item{
			ID: id, Kind: workoverview.KindRun, Status: workoverview.StatusInProgress, Running: true,
		})
	}
	schedules := make([]agenthub.ScheduleState, 0, 201)
	for i := 0; i < 201; i++ {
		id := fmt.Sprintf("schedule-%03d", i)
		schedules = append(schedules, agenthub.ScheduleState{ID: id})
		want = append(want, workoverview.Item{
			ID: id, Kind: workoverview.KindSchedule, Status: workoverview.StatusTodo,
		})
	}
	source := agenthubtest.New().WithRunning(executions...).WithSchedules(schedules...)
	service, err := agenthub.NewService(source.Dependencies(time.Unix(0, 0).UTC()))
	require.NoError(t, err)
	api, err := httpapi.New(service, httpapi.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), RequestsPerSecond: -1,
	})
	require.NoError(t, err)
	server := httptest.NewServer(api)
	defer server.Close()
	client, err := New(server.URL+httpapi.BasePath, "", server.Client())
	require.NoError(t, err)

	items, err := client.Overview(context.Background())

	require.NoError(t, err)
	require.Equal(t, want, items)
}

func TestClientOverviewStopsEndlessUniquePagination(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := requests.Add(1)
		_, _ = fmt.Fprintf(w,
			`{"items":[],"count":0,"limit":200,"next":"/api/v1/active-work?cursor=page-%d&limit=200"}`,
			page,
		)
	}))
	defer server.Close()
	client, err := New(server.URL+"/api/v1", "", server.Client())
	require.NoError(t, err)

	_, err = client.Overview(context.Background())

	require.ErrorContains(t, err, "pagination exceeds the page limit")
	require.Equal(t, int32(maxOverviewPages), requests.Load())
}

func TestClientOverviewDoesNotDisplayRemoteProblemText(t *testing.T) {
	tests := map[string]string{
		"ANSI escape":      "\x1b[2Jforged terminal output",
		"embedded newline": "first line\nforged terminal output",
		"bidi control":     "\u202eforged terminal output",
		"oversized detail": strings.Repeat("x", 4096),
	}
	for name, detail := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"title":  detail,
					"detail": detail,
				})
			}))
			defer server.Close()

			client, err := New(server.URL+"/api/v1", "", server.Client())
			require.NoError(t, err)

			_, err = client.Overview(context.Background())

			require.EqualError(t, err, "Agent Hub active-work returned HTTP 503")
		})
	}
}

func TestClientOverviewRejectsMalformedSuccessfulDocuments(t *testing.T) {
	tests := map[string]string{
		"missing items":       `{"count":0,"limit":200,"next":null}`,
		"wrong count":         `{"items":[],"count":1,"limit":200,"next":null}`,
		"missing next":        `{"items":[],"count":0,"limit":200}`,
		"incomplete item":     `{"items":[{"id":"fleet-1","type":"fleet"}],"count":1,"limit":200,"next":null}`,
		"unknown item type":   `{"items":[{"id":"fleet-1","type":"job","status":"todo","running":true}],"count":1,"limit":200,"next":null}`,
		"settled execution":   `{"items":[{"id":"fleet-1","type":"fleet","status":"done","running":false}],"count":1,"limit":200,"next":null}`,
		"failed execution":    `{"items":[{"id":"fleet-1","type":"fleet","status":"failed","running":true}],"count":1,"limit":200,"next":null}`,
		"newline in item ID":  `{"items":[{"id":"fleet-1\nforged","type":"fleet","status":"todo","running":true}],"count":1,"limit":200,"next":null}`,
		"escape in item ID":   `{"items":[{"id":"fleet-1\u001b[2J","type":"fleet","status":"todo","running":true}],"count":1,"limit":200,"next":null}`,
		"over-length item ID": fmt.Sprintf(`{"items":[{"id":"%s","type":"fleet","status":"todo","running":true}],"count":1,"limit":200,"next":null}`, strings.Repeat("x", 256)),
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
