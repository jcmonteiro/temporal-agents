package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/setting"
)

// fixedNow makes timestamps and access-log durations deterministic.
var fixedNow = time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

// viewStub is a stateful stand-in for the driving port. It holds plain domain
// objects and applies dismissal writes, so HTTP tests assert on resources and
// representations rather than on which method happened to be called.
type viewStub struct {
	fleets     []agenthub.Fleet
	runs       []agenthub.Run
	schedules  []agenthub.Schedule
	dismissals []agenthub.Dismissal
	err        error
}

func (v *viewStub) Fleets(context.Context, int) ([]agenthub.Fleet, error) {
	return v.fleets, v.err
}

func (v *viewStub) Fleet(_ context.Context, id string) (agenthub.Fleet, error) {
	if v.err != nil {
		return agenthub.Fleet{}, v.err
	}
	for _, fleet := range v.fleets {
		if fleet.ID == id {
			return fleet, nil
		}
	}
	return agenthub.Fleet{}, agenthub.ErrNotFound
}

func (v *viewStub) Runs(context.Context, int) ([]agenthub.Run, error) { return v.runs, v.err }

func (v *viewStub) Run(_ context.Context, id string) (agenthub.Run, error) {
	if v.err != nil {
		return agenthub.Run{}, v.err
	}
	for _, run := range v.runs {
		if run.ID == id {
			return run, nil
		}
	}
	return agenthub.Run{}, agenthub.ErrNotFound
}

func (v *viewStub) Schedules(context.Context, int) ([]agenthub.Schedule, error) {
	return v.schedules, v.err
}

func (v *viewStub) ActiveWork(_ context.Context, query agenthub.PageQuery) (agenthub.Page[agenthub.ActiveWorkItem], error) {
	if v.err != nil {
		return agenthub.Page[agenthub.ActiveWorkItem]{}, v.err
	}
	var all []agenthub.ActiveWorkItem
	for _, fleet := range v.fleets {
		if fleet.Running {
			all = append(all, agenthub.ActiveWorkItem{ID: fleet.ID, Type: agenthub.ActiveWorkFleet, Status: fleet.Status, Running: true, Location: fleet.Location})
		}
	}
	for _, run := range v.runs {
		if run.Running {
			all = append(all, agenthub.ActiveWorkItem{ID: run.ID, Type: activeTypeFromRun(run.Type), Status: run.Status, Running: true, Location: run.Location})
		}
	}
	for _, schedule := range v.schedules {
		all = append(all, agenthub.ActiveWorkItem{ID: schedule.ID, Type: agenthub.ActiveWorkSchedule, Status: schedule.Status, Location: schedule.Location})
	}
	offset := 0
	if len(query.Cursor) > 0 {
		parsed, err := strconv.Atoi(string(query.Cursor))
		if err != nil || parsed < 0 || parsed > len(all) {
			return agenthub.Page[agenthub.ActiveWorkItem]{}, agenthub.ErrInvalid
		}
		offset = parsed
	}
	end := min(offset+query.Limit, len(all))
	page := agenthub.Page[agenthub.ActiveWorkItem]{Items: append([]agenthub.ActiveWorkItem(nil), all[offset:end]...)}
	if end < len(all) {
		page.Next = []byte(strconv.Itoa(end))
	}
	return page, nil
}

func activeTypeFromRun(runType agenthub.RunType) agenthub.ActiveWorkType {
	switch runType {
	case agenthub.RunTypeDevelop:
		return agenthub.ActiveWorkDevelop
	case agenthub.RunTypeReview:
		return agenthub.ActiveWorkReview
	case agenthub.RunTypePilot:
		return agenthub.ActiveWorkPilot
	case agenthub.RunTypeFleetPlan:
		return agenthub.ActiveWorkFleetPlan
	default:
		return agenthub.ActiveWorkRun
	}
}

func (v *viewStub) Dismissals(context.Context) ([]agenthub.Dismissal, error) {
	return v.dismissals, v.err
}

func (v *viewStub) Dismiss(_ context.Context, kind agenthub.ItemKind, itemID string) (agenthub.Dismissal, error) {
	if v.err != nil {
		return agenthub.Dismissal{}, v.err
	}
	if err := agenthub.ValidateDismissalTarget(kind, itemID); err != nil {
		return agenthub.Dismissal{}, err
	}
	var terminal bool
	switch kind {
	case agenthub.KindFleet:
		for _, fleet := range v.fleets {
			terminal = terminal || fleet.ID == itemID && fleet.Dismissible()
		}
	case agenthub.KindRun:
		for _, run := range v.runs {
			terminal = terminal || run.ID == itemID && run.Dismissible()
		}
	}
	if !terminal {
		return agenthub.Dismissal{}, agenthub.ErrNotDismissible
	}
	d := agenthub.Dismissal{Kind: kind, ItemID: itemID, DismissedAt: fixedNow}
	for _, existing := range v.dismissals {
		if existing.ID() == d.ID() {
			return existing, nil
		}
	}
	v.dismissals = append(v.dismissals, d)
	return d, nil
}

func (v *viewStub) Undismiss(_ context.Context, kind agenthub.ItemKind, itemID string) error {
	if v.err != nil {
		return v.err
	}
	id := agenthub.Dismissal{Kind: kind, ItemID: itemID}.ID()
	for i, dismissal := range v.dismissals {
		if dismissal.ID() == id {
			v.dismissals = append(v.dismissals[:i], v.dismissals[i+1:]...)
			return nil
		}
	}
	return agenthub.ErrNotFound
}

// newTestServer builds a server with logging discarded and rate limiting disabled,
// so a test only observes the behavior it is about.
func newTestServer(t *testing.T, view WorkView, mutate ...func(*Options)) *Server {
	t.Helper()
	options := Options{
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		RequestsPerSecond: -1,
		Now:               func() time.Time { return fixedNow },
		WebDir:            "",
		// The tests that are about resources ask for an open server on purpose, which
		// is the only way to get one: a server that neither authenticates nor was asked
		// not to does not build (see TestAServerThatNeitherAuthenticatesNorWasAskedNotToDoesNotBuild).
		AllowUnauthenticated: true,
		// Every deployment that publishes configuration serves the settings resource, so
		// the default test server has one: a resource served by production and by no test
		// would be a resource nothing keeps honest.
		Settings: settingsStub{},
		// A hub that can be worked in can be started in, so the default test server
		// offers the start surface: a route served by production and by no test is a
		// route nothing keeps honest.
		Start: &starterStub{},
		// The same reasoning applies to the place registry: a deployment that can be
		// worked in publishes it, so the default test server does too.
		Places: &placesStub{},
	}
	for _, change := range mutate {
		change(&options)
	}
	server, err := New(view, options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server
}

// newRequest builds a request for the loopback host trusted by the test server.
func newRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Host = "localhost"
	return req
}

// request runs one request through server.
func request(t *testing.T, server http.Handler, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, newRequest(method, target, body))
	return response
}

// TestFleetCollectionPublishesAPortableContract is the transport's primary read
// behavior: a domain fleet becomes a DB-agnostic resource with a kind discriminator,
// server-owned status and progress, UTC timestamps, a schema link and an entity tag.
func TestFleetCollectionPublishesAPortableContract(t *testing.T) {
	started := time.Date(2026, 8, 6, 10, 0, 0, 123, time.FixedZone("west", -4*60*60))
	view := &viewStub{fleets: []agenthub.Fleet{{
		ID: "fleet-1", Goal: "Expose pricing", Status: agenthub.StatusInProgress,
		Progress: agenthub.Progress{Done: 1, Total: 3}, StartedAt: started,
		Nodes: []agenthub.FleetNode{{ID: "api", Status: agenthub.StatusTodo}},
	}}}
	server := newTestServer(t, view)

	response := request(t, server, http.MethodGet, BasePath+"/fleets?limit=7", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", contentType)
	}
	if !strings.Contains(response.Header().Get("Link"), "/schemas/fleet-collection.v1") {
		t.Errorf("Link = %q, want the versioned collection schema", response.Header().Get("Link"))
	}
	if response.Header().Get("ETag") == "" {
		t.Error("ETag is empty")
	}
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	var document struct {
		Items []fleetResource `json:"items"`
		Count int             `json:"count"`
		Limit int             `json:"limit"`
	}
	decodeResponse(t, response, &document)
	if document.Count != 1 || document.Limit != 7 || len(document.Items) != 1 {
		t.Fatalf("collection = count %d, limit %d, items %d; want 1/7/1", document.Count, document.Limit, len(document.Items))
	}
	fleet := document.Items[0]
	if fleet.Kind != agenthub.KindFleet || fleet.Status != agenthub.StatusInProgress {
		t.Errorf("fleet kind/status = %q/%q, want fleet/in-progress", fleet.Kind, fleet.Status)
	}
	if fleet.Progress.Done != 1 || fleet.Progress.Total != 3 || fleet.Progress.Fraction != 0.333 {
		t.Errorf("progress = %+v, want 1/3/0.333", fleet.Progress)
	}
	if fleet.StartedAt == nil || *fleet.StartedAt != "2026-08-06T14:00:00Z" {
		t.Errorf("startedAt = %v, want UTC RFC 3339 without local offset", fleet.StartedAt)
	}
	if fleet.EndedAt != nil {
		t.Errorf("endedAt = %v, want null while running", fleet.EndedAt)
	}
	if fleet.Nodes != nil {
		t.Errorf("collection carries %d nodes, want the graph only on the item resource", len(fleet.Nodes))
	}
}

func TestActiveWorkPublishesAnAdditivePagedResource(t *testing.T) {
	view := &viewStub{
		fleets: []agenthub.Fleet{
			{ID: "fleet-finished", Status: agenthub.StatusDone},
			{ID: "fleet-running", Running: true, Status: agenthub.StatusInProgress},
		},
		runs: []agenthub.Run{{
			ID: "review-running", Type: agenthub.RunTypeReview,
			Running: true, Status: agenthub.StatusInProgress,
		}},
	}
	server := newTestServer(t, view)

	response := request(t, server, http.MethodGet, BasePath+"/active-work?limit=1", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var first activeWorkCollection
	decodeResponse(t, response, &first)
	if len(first.Items) != 1 || first.Items[0].ID != "fleet-running" || first.Items[0].Status != agenthub.StatusInProgress || !first.Items[0].Running || first.Next == nil {
		t.Fatalf("first active-work page = %+v, want the in-progress fleet and a next link", first)
	}

	response = request(t, server, http.MethodGet, *first.Next, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("next status = %d: %s", response.Code, response.Body.String())
	}
	var second activeWorkCollection
	decodeResponse(t, response, &second)
	if len(second.Items) != 1 || second.Items[0].ID != "review-running" || second.Items[0].Type != agenthub.ActiveWorkReview || second.Next != nil {
		t.Fatalf("second active-work page = %+v, want the final review run", second)
	}
}

func TestActiveWorkRejectsMalformedAndForeignCursors(t *testing.T) {
	server := newTestServer(t, &viewStub{})
	for _, query := range []string{"cursor=", "cursor=%20"} {
		empty := request(t, server, http.MethodGet, BasePath+"/active-work?"+query, nil)
		requireProblem(t, empty, http.StatusBadRequest, codeInvalidRequest)
	}

	malformed := request(t, server, http.MethodGet, BasePath+"/active-work?cursor=not-base64!", nil)
	requireProblem(t, malformed, http.StatusBadRequest, codeInvalidRequest)

	malformedEncodingRequest := newRequest(http.MethodGet, BasePath+"/active-work", nil)
	malformedEncodingRequest.URL.RawQuery = "cursor=%%%"
	malformedEncoding := httptest.NewRecorder()
	server.ServeHTTP(malformedEncoding, malformedEncodingRequest)
	requireProblem(t, malformedEncoding, http.StatusBadRequest, codeInvalidRequest)

	for _, query := range []string{
		"cursor=YQ&cursor=Yg",
		"limit=1&limit=2",
	} {
		repeated := request(t, server, http.MethodGet, BasePath+"/active-work?"+query, nil)
		requireProblem(t, repeated, http.StatusBadRequest, codeInvalidRequest)
	}

	oldFleetCursor := base64.RawURLEncoding.EncodeToString([]byte("2026-08-06T12:00:00Z\nfleet-1"))
	foreign := request(t, server, http.MethodGet, BasePath+"/active-work?cursor="+oldFleetCursor, nil)
	requireProblem(t, foreign, http.StatusBadRequest, codeInvalidRequest)
}

func TestExistingV1FleetCollectionDoesNotGainActiveWorkFields(t *testing.T) {
	view := &viewStub{fleets: []agenthub.Fleet{{
		ID: "fleet-running", Running: true, Status: agenthub.StatusInProgress,
	}}}
	response := request(t, newTestServer(t, view), http.MethodGet, BasePath+"/fleets", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var document map[string]any
	decodeResponse(t, response, &document)
	if _, exists := document["next"]; exists {
		t.Error("the existing fleet collection gained a next field")
	}
	items, _ := document["items"].([]any)
	fleet, _ := items[0].(map[string]any)
	if _, exists := fleet["running"]; exists {
		t.Error("the existing fleet model gained a running field")
	}
}

// TestFleetResourceIncludesTheNodeGraph pins the endpoint PR #19's dedicated fleet
// view consumes: node labels, dependency edges, statuses, and child executions.
func TestFleetResourceIncludesTheNodeGraph(t *testing.T) {
	started := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	view := &viewStub{fleets: []agenthub.Fleet{{
		ID: "fleet-1", Goal: "Expose pricing", Status: agenthub.StatusInProgress,
		Progress: agenthub.Progress{Done: 1, Total: 2}, StartedAt: started,
		Nodes: []agenthub.FleetNode{
			{ID: "domain", Prompt: "Define the model", Status: agenthub.StatusDone, Execution: &agenthub.NodeExecution{
				WorkflowID: "fleet-1-domain", RunID: "r1", StartedAt: started, EndedAt: started.Add(time.Minute), Tokens: 10,
			}},
			{ID: "api", Prompt: "Publish REST", DependsOn: []string{"domain"}, Status: agenthub.StatusTodo},
		},
	}}}
	response := request(t, newTestServer(t, view), http.MethodGet, BasePath+"/fleets/fleet-1", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var fleet fleetResource
	decodeResponse(t, response, &fleet)
	if len(fleet.Nodes) != 2 || fleet.Nodes[1].DependsOn[0] != "domain" {
		t.Fatalf("nodes = %+v, want api depending on domain", fleet.Nodes)
	}
	if fleet.Nodes[0].Execution == nil || fleet.Nodes[0].Execution.WorkflowID != "fleet-1-domain" {
		t.Errorf("domain execution = %+v, want fleet-1-domain", fleet.Nodes[0].Execution)
	}
	if fleet.Nodes[1].Execution != nil {
		t.Errorf("unstarted node execution = %+v, want null", fleet.Nodes[1].Execution)
	}
}

// TestRunChainsAndSchedulesUseUniformDiscriminators pins the frontend composition
// seam: all three collections expose id, kind, label and status, while retaining
// their honest kind-specific facts.
func TestRunChainsAndSchedulesUseUniformDiscriminators(t *testing.T) {
	view := &viewStub{
		runs: []agenthub.Run{{
			ID: "run-1", Type: agenthub.RunTypePrompt, Label: "Daily digest",
			Status: agenthub.StatusDone, StartedAt: fixedNow.Add(-time.Hour), EndedAt: fixedNow,
			Iterations: 3, Tokens: 99,
		}},
		schedules: []agenthub.Schedule{{
			ID: "schedule-1", Label: "Post digest", Spec: "0 9 * * *",
			Status: agenthub.StatusPaused, Paused: true, NextRunAt: fixedNow.Add(time.Hour),
		}},
	}
	server := newTestServer(t, view)

	runs := request(t, server, http.MethodGet, BasePath+"/runs", nil)
	var runCollection locatedCollection[runResource]
	decodeResponse(t, runs, &runCollection)
	if len(runCollection.Items) != 1 {
		t.Fatalf("runs = %d, want 1", len(runCollection.Items))
	}
	run := runCollection.Items[0]
	if run.Kind != agenthub.KindRun || run.Iterations != 3 || !run.Dismissible {
		t.Errorf("run = %+v, want a terminal three-iteration run", run)
	}

	schedules := request(t, server, http.MethodGet, BasePath+"/schedules", nil)
	var scheduleCollection locatedCollection[scheduleResource]
	decodeResponse(t, schedules, &scheduleCollection)
	if len(scheduleCollection.Items) != 1 {
		t.Fatalf("schedules = %d, want 1", len(scheduleCollection.Items))
	}
	schedule := scheduleCollection.Items[0]
	if schedule.Kind != agenthub.KindSchedule || !schedule.Paused || schedule.Dismissible {
		t.Errorf("schedule = %+v, want paused and never dismissible", schedule)
	}
}

// TestConditionalGetAvoidsSendingAnUnchangedBody pins cheap polling: the entity tag
// from one live read can be revalidated and returns 304 with no body.
func TestConditionalGetAvoidsSendingAnUnchangedBody(t *testing.T) {
	server := newTestServer(t, &viewStub{})
	first := request(t, server, http.MethodGet, BasePath+"/runs", nil)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("first response has no ETag")
	}
	req := newRequest(http.MethodGet, BasePath+"/runs", nil)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	server.ServeHTTP(second, req)
	if second.Code != http.StatusNotModified || second.Body.Len() != 0 {
		t.Fatalf("conditional response = %d with %d bytes, want 304 with none", second.Code, second.Body.Len())
	}
}

// TestHeadHasTheGetHeadersAndNoBody pins standard HTTP metadata discovery.
func TestHeadHasTheGetHeadersAndNoBody(t *testing.T) {
	response := request(t, newTestServer(t, &viewStub{}), http.MethodHead, BasePath+"/runs", nil)
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("HEAD = %d with %d bytes, want 200 with no body", response.Code, response.Body.Len())
	}
	if response.Header().Get("ETag") == "" || response.Header().Get("Content-Length") == "" {
		t.Errorf("HEAD metadata = ETag %q, Content-Length %q; want both", response.Header().Get("ETag"), response.Header().Get("Content-Length"))
	}
}

// TestInvalidLimitIsAResolvableProblem pins structured refusal: the error has a
// stable type URI a consumer can follow and a request ID it can report.
func TestInvalidLimitIsAResolvableProblem(t *testing.T) {
	server := newTestServer(t, &viewStub{})
	response := request(t, server, http.MethodGet, BasePath+"/runs?limit=201", nil)
	problem := requireProblem(t, response, http.StatusBadRequest, codeInvalidRequest)
	if problem.RequestID == "" || response.Header().Get(requestIDHeader) != problem.RequestID {
		t.Errorf("request IDs = header %q, body %q; want the same non-empty value", response.Header().Get(requestIDHeader), problem.RequestID)
	}
	if !strings.Contains(problem.Detail, "at most 200") {
		t.Errorf("detail = %q, want the published cap", problem.Detail)
	}

	description := request(t, server, http.MethodGet, problem.Type, nil)
	if description.Code != http.StatusOK || !strings.Contains(description.Body.String(), "request") {
		t.Errorf("problem type URI did not resolve: %d %s", description.Code, description.Body.String())
	}
}

// TestUnknownAndWrongMethodAreProblemDocuments pins that no API failure falls back
// to a plain-text default handler.
func TestUnknownAndWrongMethodAreProblemDocuments(t *testing.T) {
	server := newTestServer(t, &viewStub{})
	requireProblem(t, request(t, server, http.MethodGet, BasePath+"/nothing", nil), http.StatusNotFound, codeNotFound)
	// The schedules are read-only: creating one needs a recurrence editor, which is
	// a surface of its own.
	wrong := request(t, server, http.MethodPost, BasePath+"/schedules", strings.NewReader("{}"))
	requireProblem(t, wrong, http.StatusMethodNotAllowed, codeMethodNotAllowed)
	if got := wrong.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want GET, HEAD", got)
	}
}

// TestDismissalLifecycle pins the API's sole mutation end to end: strict JSON creates
// durable view state under an address, listing shows it, and DELETE removes it.
func TestDismissalLifecycle(t *testing.T) {
	view := &viewStub{runs: []agenthub.Run{{ID: "run-1", Status: agenthub.StatusDone, Iterations: 1}}}
	server := newTestServer(t, view)

	body := strings.NewReader(`{"kind":"run","itemId":"run-1"}`)
	req := newRequest(http.MethodPost, BasePath+"/dismissals", body)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	created := httptest.NewRecorder()
	server.ServeHTTP(created, req)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", created.Code, created.Body.String())
	}
	if got := created.Header().Get("Location"); got != BasePath+"/dismissals/run:run-1" {
		t.Errorf("Location = %q, want the dismissal resource", got)
	}
	var dismissal dismissalResource
	decodeResponse(t, created, &dismissal)
	if dismissal.ID != "run:run-1" || dismissal.DismissedAt == nil {
		t.Errorf("dismissal = %+v, want run:run-1 with a timestamp", dismissal)
	}

	listed := request(t, server, http.MethodGet, BasePath+"/dismissals", nil)
	var collection collection[dismissalResource]
	decodeResponse(t, listed, &collection)
	if collection.Count != 1 || collection.Items[0].ID != dismissal.ID {
		t.Fatalf("dismissals = %+v, want the created one", collection)
	}

	deleted := request(t, server, http.MethodDelete, created.Header().Get("Location"), nil)
	if deleted.Code != http.StatusNoContent || deleted.Body.Len() != 0 {
		t.Fatalf("delete = %d with %q, want 204 empty", deleted.Code, deleted.Body.String())
	}
	requireProblem(t, request(t, server, http.MethodDelete, created.Header().Get("Location"), nil), http.StatusNotFound, codeNotFound)
}

// TestDismissalBodyIsStrictAndBounded pins the public-input boundary: media type,
// unknown fields, trailing documents and size are refused before the core sees them.
func TestDismissalBodyIsStrictAndBounded(t *testing.T) {
	view := &viewStub{runs: []agenthub.Run{{ID: "run-1", Status: agenthub.StatusDone, Iterations: 1}}}
	server := newTestServer(t, view, func(options *Options) { options.MaxBodyBytes = 48 })

	cases := []struct {
		name        string
		contentType string
		body        string
		status      int
		code        problemCode
	}{
		{"missing media type", "", `{"kind":"run","itemId":"run-1"}`, 415, codeUnsupportedMediaType},
		{"unknown field", "application/json", `{"kind":"run","itemId":"run-1","owner":"me"}`, 400, codeInvalidRequest},
		{"two documents", "application/json", `{"kind":"run","itemId":"run-1"}{}`, 400, codeInvalidRequest},
		{"oversized", "application/json", `{"kind":"run","itemId":"run-111111111111111111111111111111111111111111111"}`, 413, codeRequestTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := newRequest(http.MethodPost, BasePath+"/dismissals", strings.NewReader(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			requireProblem(t, response, tc.status, tc.code)
		})
	}
}

// TestActiveItemCannotBeDismissed pins that the server enforces the rule rather than
// trusting a frontend to hide the affordance.
func TestActiveItemCannotBeDismissed(t *testing.T) {
	view := &viewStub{runs: []agenthub.Run{{ID: "run-1", Running: true, Status: agenthub.StatusInProgress, Iterations: 1}}}
	server := newTestServer(t, view)
	req := newRequest(http.MethodPost, BasePath+"/dismissals", strings.NewReader(`{"kind":"run","itemId":"run-1"}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	requireProblem(t, response, http.StatusConflict, codeNotDismissible)
}

func TestARecordedPlaceThatCannotBeExpressedIsReportedAsADefectHere(t *testing.T) {
	// A location is built from a fact the system recorded, never from the request, so a
	// consumer can do nothing about one that is invalid and must not be told to change
	// its request. The recorded path would also be a server path: it belongs in the log
	// an operator reads, not in a document a consumer receives.
	cause := errors.Join(agenthub.ErrInvalidLocation,
		errors.New(`the location directory "srv/work/pricing" must be absolute`))
	var logged bytes.Buffer
	server := newTestServer(t, &viewStub{err: cause}, func(options *Options) {
		options.Logger = slog.New(slog.NewTextHandler(&logged, nil))
	})

	response := request(t, server, http.MethodGet, BasePath+"/runs", nil)

	problem := requireProblem(t, response, http.StatusInternalServerError, codeInternal)
	if problem.Detail != "" {
		t.Errorf("problem detail = %q, want nothing about a place the consumer never sent", problem.Detail)
	}
	if strings.Contains(response.Body.String(), "srv/work/pricing") {
		t.Errorf("the problem document leaked the recorded path: %s", response.Body.String())
	}
	if !strings.Contains(logged.String(), "srv/work/pricing") || !strings.Contains(logged.String(), BasePath+"/runs") {
		t.Errorf("the log records neither the recorded place nor the path it was read on: %s", logged.String())
	}
	// The mapping is exhaustive by intent, so a failure the core does classify must not
	// be recorded as one it does not: an operator triaging "unclassified" is looking for
	// a missing branch that is right there.
	if strings.Contains(logged.String(), "unclassified") {
		t.Errorf("a classified failure was logged as unclassified: %s", logged.String())
	}
}

// TestDependencyFailureIsRetryableAndDoesNotLeakTheCause pins both reliability and
// security: a partial overview is never returned, Retry-After is present, and the
// driver's message remains only in the server log.
func TestDependencyFailureIsRetryableAndDoesNotLeakTheCause(t *testing.T) {
	cause := errors.Join(agenthub.ErrUnavailable, errors.New("postgres://user:secret@host/database"))
	response := request(t, newTestServer(t, &viewStub{err: cause}), http.MethodGet, BasePath+"/runs", nil)
	problem := requireProblem(t, response, http.StatusServiceUnavailable, codeDependencyUnavailable)
	if response.Header().Get("Retry-After") == "" {
		t.Error("Retry-After is empty")
	}
	if strings.Contains(problem.Detail, "secret") || strings.Contains(response.Body.String(), "postgres") {
		t.Errorf("problem leaked its internal cause: %s", response.Body.String())
	}
}

// TestHostMustBeExplicitlyAllowed pins the DNS-rebinding boundary: a hostname that
// resolves to loopback is still refused unless the server configuration names it.
func TestHostMustBeExplicitlyAllowed(t *testing.T) {
	server := newTestServer(t, &viewStub{})
	req := newRequest(http.MethodGet, BasePath+"/runs", nil)
	req.Host = "attacker.example:8973"
	response := httptest.NewRecorder()

	server.ServeHTTP(response, req)

	if response.Code != http.StatusForbidden {
		t.Fatalf("hostile Host status = %d, want 403", response.Code)
	}
}

// TestCORSRejectsUnlistedOriginsAndAllowsExactMatches pins that a supplied Origin
// is an access decision, not only a response-header decision.
func TestCORSRejectsUnlistedOriginsAndAllowsExactMatches(t *testing.T) {
	view := &viewStub{}
	denied := newTestServer(t, view)
	req := newRequest(http.MethodGet, BasePath+"/runs", nil)
	req.Header.Set("Origin", "https://hostile.example")
	response := httptest.NewRecorder()
	denied.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unlisted Origin status = %d, want 403", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("default Access-Control-Allow-Origin = %q, want none", got)
	}

	allowed := newTestServer(t, view, func(options *Options) {
		options.AllowedOrigins = []string{"https://hub.example", "*"}
	})
	for origin, want := range map[string]struct {
		status      int
		allowOrigin string
	}{
		"https://hub.example":     {status: http.StatusNoContent, allowOrigin: "https://hub.example"},
		"https://hostile.example": {status: http.StatusForbidden},
	} {
		req := newRequest(http.MethodOptions, BasePath+"/runs", nil)
		req.Header.Set("Origin", origin)
		res := httptest.NewRecorder()
		allowed.ServeHTTP(res, req)
		if res.Code != want.status {
			t.Errorf("preflight for %s = %d, want %d", origin, res.Code, want.status)
		}
		if got := res.Header().Get("Access-Control-Allow-Origin"); got != want.allowOrigin {
			t.Errorf("origin %s allowed as %q, want %q", origin, got, want.allowOrigin)
		}
	}
}

// TestBearerAuthenticationProtectsResources pins the protection used when the
// composition root exposes the listener beyond loopback.
func TestBearerAuthenticationProtectsResources(t *testing.T) {
	server := newTestServer(t, &viewStub{}, func(options *Options) {
		options.AuthToken = "correct horse battery staple"
	})
	for name, want := range map[string]struct {
		authorization string
		status        int
	}{
		"missing": {status: http.StatusUnauthorized},
		"wrong":   {authorization: "Bearer wrong", status: http.StatusUnauthorized},
		"valid":   {authorization: "Bearer correct horse battery staple", status: http.StatusOK},
	} {
		t.Run(name, func(t *testing.T) {
			req := newRequest(http.MethodGet, BasePath+"/runs", nil)
			req.Header.Set("Authorization", want.authorization)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			if want.status == http.StatusUnauthorized {
				requireProblem(t, response, http.StatusUnauthorized, codeAuthenticationRequired)
				if response.Header().Get("WWW-Authenticate") == "" {
					t.Fatal("WWW-Authenticate is empty")
				}
				return
			}
			if response.Code != want.status {
				t.Fatalf("status = %d, want %d", response.Code, want.status)
			}
		})
	}
}

// TestHealthReportsEveryDependencyWithoutItsError pins an operational contract that
// is useful globally: a monitor sees which dependency is down, while credentials and
// driver details stay in the log.
func TestHealthReportsEveryDependencyWithoutItsError(t *testing.T) {
	server := newTestServer(t, &viewStub{}, func(options *Options) {
		options.HealthChecks = []HealthCheck{
			{Name: "temporal", Check: func(context.Context) error { return nil }},
			{Name: "database", Check: func(context.Context) error { return errors.New("password=secret") }},
		}
	})
	response := request(t, server, http.MethodGet, BasePath+"/health", nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("health = %d, want 503", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/health+json" {
		t.Errorf("Content-Type = %q, want application/health+json", got)
	}
	if !strings.Contains(response.Body.String(), `"database":{"status":"fail"}`) || strings.Contains(response.Body.String(), "secret") {
		t.Errorf("health body = %s, want a failed database with no cause", response.Body.String())
	}
}

// TestTheSpecificationAndSchemasAreDiscoverable pins the contract-based surface:
// the API entry point links to a valid OpenAPI document and a standalone schema whose
// references resolve within itself.
func TestTheSpecificationAndSchemasAreDiscoverable(t *testing.T) {
	server := newTestServer(t, &viewStub{})
	description := request(t, server, http.MethodGet, BasePath, nil)
	if description.Code != http.StatusOK || !strings.Contains(description.Body.String(), BasePath+"/openapi.json") {
		t.Fatalf("description = %d %s", description.Code, description.Body.String())
	}

	specification := request(t, server, http.MethodGet, BasePath+"/openapi.json", nil)
	if specification.Code != http.StatusOK || !strings.HasPrefix(specification.Header().Get("Content-Type"), "application/vnd.oai.openapi+json") {
		t.Fatalf("specification = %d %q", specification.Code, specification.Header().Get("Content-Type"))
	}
	var spec map[string]any
	decodeResponse(t, specification, &spec)
	if spec["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", spec["openapi"])
	}

	schema := request(t, server, http.MethodGet, BasePath+"/schemas/fleet.v1", nil)
	if schema.Code != http.StatusOK || schema.Header().Get("Content-Type") != schemaMediaType {
		t.Fatalf("schema = %d %q", schema.Code, schema.Header().Get("Content-Type"))
	}
	var document map[string]any
	decodeResponse(t, schema, &document)
	if document["$schema"] != "https://json-schema.org/draft/2020-12/schema" || document["$defs"] == nil {
		t.Errorf("schema is not standalone: keys %v", sortedKeys(document))
	}
	encoded, _ := json.Marshal(document)
	if bytes.Contains(encoded, []byte("#/components/schemas/")) {
		t.Error("standalone schema still contains an OpenAPI component reference")
	}
}

// TestEveryServedRouteIsSpecified pins that implementation and contract move as one:
// no resource can be added to either side without adding it to the other.
//
// The server is wired with a sign-in service, because the specification describes
// the whole contract: a deployment that configures no identity provider serves fewer
// routes than it documents, which is a deployment decision and not a drift.
func TestEveryServedRouteIsSpecified(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})
	paths, ok := server.spec.document["paths"].(map[string]any)
	if !ok {
		t.Fatal("specification paths are missing")
	}
	served := map[string]map[string]bool{}
	for _, resource := range server.resources() {
		if resource.undocumented {
			continue
		}
		served[resource.pattern] = map[string]bool{}
		for method := range resource.methods {
			served[resource.pattern][strings.ToLower(method)] = true
		}
	}
	if len(served) != len(paths) {
		t.Errorf("served %d documented paths, specification has %d", len(served), len(paths))
	}
	for path, methods := range served {
		documented, ok := paths[path].(map[string]any)
		if !ok {
			t.Errorf("served path %s is not in the specification", path)
			continue
		}
		for method := range methods {
			if _, ok := documented[method]; !ok {
				t.Errorf("served operation %s %s is not in the specification", strings.ToUpper(method), path)
			}
		}
	}
	for path := range paths {
		if served[path] == nil {
			t.Errorf("specified path %s is not served", path)
		}
	}
}

// TestTheSpecificationModelsBothCredentialKinds pins that a generated client knows
// how to authenticate whichever it is: a script with the configured token, a browser
// with the session it receives after signing in.
func TestTheSpecificationModelsBothCredentialKinds(t *testing.T) {
	server := newTestServer(t, &viewStub{})
	components := server.spec.document["components"].(map[string]any)
	securitySchemes := components["securitySchemes"].(map[string]any)
	bearer := securitySchemes["bearerAuth"].(map[string]any)
	if bearer["type"] != "http" || bearer["scheme"] != "bearer" {
		t.Fatalf("bearerAuth = %+v, want an HTTP bearer scheme", bearer)
	}
	session := securitySchemes["sessionCookie"].(map[string]any)
	if session["type"] != "apiKey" || session["in"] != "cookie" || session["name"] != sessionCookieName {
		t.Fatalf("sessionCookie = %+v, want the session cookie this API sets", session)
	}
	security := server.spec.document["security"].([]any)
	if len(security) != 3 {
		t.Fatalf("security = %+v, want anonymous loopback, the bearer token, or a session", security)
	}
	paths := server.spec.document["paths"].(map[string]any)
	for path, rawPath := range paths {
		if handsOutACredential(path) {
			// A route whose purpose is to hand out a credential cannot answer 401: being
			// unauthenticated is the normal way to arrive at it.
			continue
		}
		for method, rawOperation := range rawPath.(map[string]any) {
			if method == "parameters" {
				continue
			}
			responses := rawOperation.(map[string]any)["responses"].(map[string]any)
			if _, ok := responses["401"]; !ok {
				t.Errorf("%s %s has no 401 response", strings.ToUpper(method), path)
			}
		}
	}
}

// handsOutACredential reports the two routes a browser reaches before it has one.
func handsOutACredential(path string) bool {
	return path == BasePath+"/auth/sign-in" || path == BasePath+"/auth/callback"
}

// TestTheSpecificationUsesTheCoreVocabularies pins the data model to the contract:
// a status or kind added in the core cannot be absent from the schema a consumer
// generates code from.
func TestTheSpecificationUsesTheCoreVocabularies(t *testing.T) {
	server := newTestServer(t, &viewStub{})
	workStatus := schemaEnum(t, server.spec.schemas, "WorkStatus")
	wantStatuses := make([]string, 0, len(agenthub.WorkStatuses()))
	for _, status := range agenthub.WorkStatuses() {
		wantStatuses = append(wantStatuses, string(status))
	}
	assertSameStrings(t, workStatus, wantStatuses)

	itemKind := schemaEnum(t, server.spec.schemas, "ItemKind")
	wantKinds := make([]string, 0, len(agenthub.ItemKinds()))
	for _, kind := range agenthub.ItemKinds() {
		wantKinds = append(wantKinds, string(kind))
	}
	assertSameStrings(t, itemKind, wantKinds)

	activeWorkType := schemaEnum(t, server.spec.schemas, "ActiveWorkType")
	wantActiveTypes := make([]string, 0, len(agenthub.ActiveWorkTypes()))
	for _, workType := range agenthub.ActiveWorkTypes() {
		wantActiveTypes = append(wantActiveTypes, string(workType))
	}
	assertSameStrings(t, activeWorkType, wantActiveTypes)
}

// TestWellKnownCatalogPointsAtTheContract pins host-level discovery.
func TestWellKnownCatalogPointsAtTheContract(t *testing.T) {
	response := request(t, newTestServer(t, &viewStub{}), http.MethodGet, "/.well-known/api-catalog", nil)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/linkset+json" {
		t.Fatalf("catalog = %d %q", response.Code, response.Header().Get("Content-Type"))
	}
	for _, want := range []string{BasePath, BasePath + "/openapi.json", BasePath + "/health"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("catalog does not contain %q: %s", want, response.Body.String())
		}
	}
}

// TestStaticAssetPathsCannotTraverseTheWebDirectory pins the filesystem boundary for
// both slash and encoded-backslash traversal attempts.
func TestStaticAssetPathsCannotTraverseTheWebDirectory(t *testing.T) {
	parent := t.TempDir()
	webDir := filepath.Join(parent, "web")
	if err := os.Mkdir(webDir, 0o700); err != nil {
		t.Fatalf("create web directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write file outside web directory: %v", err)
	}
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.WebDir = webDir })

	for _, target := range []string{"/../secret.txt", "/..%5csecret.txt"} {
		req := newRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		server.serveAsset(response, req)
		requireProblem(t, response, http.StatusNotFound, codeNotFound)
		if response.Body.String() == "secret" {
			t.Fatalf("%s exposed a file outside the web directory", target)
		}
	}
}

// TestStaticAssetsAreOptionalAndNeverCaptureAPIPaths pins hosting decoupling: a built
// bundle can be served for local convenience, deep links fall back to its index, and
// an unknown API endpoint remains a JSON problem rather than the SPA.
func TestStaticAssetsAreOptionalAndNeverCaptureAPIPaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<main>Agent Hub</main>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app-a1b2.js"), []byte("console.log('hub')"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.WebDir = dir })

	deepLink := request(t, server, http.MethodGet, "/fleets/fleet-1", nil)
	if deepLink.Code != http.StatusOK || !strings.Contains(deepLink.Body.String(), "Agent Hub") {
		t.Fatalf("deep link = %d %s", deepLink.Code, deepLink.Body.String())
	}
	if deepLink.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("index Cache-Control = %q, want no-cache", deepLink.Header().Get("Cache-Control"))
	}
	asset := request(t, server, http.MethodGet, "/app-a1b2.js", nil)
	if !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset Cache-Control = %q, want immutable", asset.Header().Get("Cache-Control"))
	}
	requireProblem(t, request(t, server, http.MethodGet, BasePath+"/unknown", nil), http.StatusNotFound, codeNotFound)
}

// TestNewRequiresTheDrivingPort pins the hexagon's composition boundary.
func TestNewRequiresTheDrivingPort(t *testing.T) {
	if _, err := New(nil, Options{}); err == nil {
		t.Fatal("New(nil) = nil error, want a refusal")
	}
}

// requireProblem asserts a problem response and returns its document.
func requireProblem(t *testing.T, response *httptest.ResponseRecorder, status int, code problemCode) problemDocument {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != problemMediaType {
		t.Fatalf("Content-Type = %q, want %q", got, problemMediaType)
	}
	var document problemDocument
	decodeResponse(t, response, &document)
	if document.Type != BasePath+"/problems/"+string(code) || document.Status != status {
		t.Errorf("problem = type %q status %d, want %s and %d", document.Type, document.Status, code, status)
	}
	return document
}

// decodeResponse decodes the response body into target.
func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, response.Body.String())
	}
}

// schemaEnum reads one schema's string enum.
func schemaEnum(t *testing.T, schemas map[string]any, name string) []string {
	t.Helper()
	schema, ok := schemas[name].(map[string]any)
	if !ok {
		t.Fatalf("schema %s is missing", name)
	}
	values, ok := schema["enum"].([]any)
	if !ok {
		t.Fatalf("schema %s has no enum", name)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.(string))
	}
	return out
}

// assertSameStrings compares two string sets.
func assertSameStrings(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// sortedKeys renders map keys for a failure message.
func sortedKeys(document map[string]any) []string {
	keys := make([]string, 0, len(document))
	for key := range document {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// TestOverviewResourcesKeepTheFieldNamesTheWebClientReads pins the wire names the
// frontend depends on. web/src/clients/api.ts is a hand-written copy of these
// resources, so a rename here silently empties the Overview: the frontend's own
// tests use fixtures written from the same assumption and cannot see the drift.
// The lists below are exactly what web/src/clients/mapping.ts reads.
func TestOverviewResourcesKeepTheFieldNamesTheWebClientReads(t *testing.T) {
	view := &viewStub{
		fleets: []agenthub.Fleet{{
			ID: "fleet-1", Goal: "Expose pricing", Status: agenthub.StatusInProgress,
			Progress: agenthub.Progress{Done: 1, Total: 3}, StartedAt: fixedNow,
			Nodes: []agenthub.FleetNode{{ID: "api", Status: agenthub.StatusTodo}},
		}},
		runs: []agenthub.Run{{
			ID: "run-1", Type: agenthub.RunTypePrompt, Label: "Daily digest",
			Status: agenthub.StatusDone, StartedAt: fixedNow, EndedAt: fixedNow, Iterations: 3,
		}},
		schedules: []agenthub.Schedule{{
			ID: "schedule-1", Label: "Post digest", Spec: "0 9 * * *",
			Status: agenthub.StatusPaused, Paused: true,
		}},
	}
	server := newTestServer(t, view)

	for _, resource := range []struct {
		path      string
		itemKeys  []string
		nestedKey string
		nested    []string
	}{
		{
			path:      "/fleets",
			itemKeys:  []string{"id", "kind", "label", "status", "progress", "dismissible", "upNext", "locationId"},
			nestedKey: "progress",
			nested:    []string{"done", "total", "fraction"},
		},
		{
			path:     "/runs",
			itemKeys: []string{"id", "kind", "type", "label", "status", "iterations", "dismissible", "locationId"},
		},
		{
			path:     "/schedules",
			itemKeys: []string{"id", "kind", "label", "spec", "status", "paused", "dismissible", "locationId"},
		},
	} {
		response := request(t, server, http.MethodGet, BasePath+resource.path, nil)
		var document map[string]any
		decodeResponse(t, response, &document)

		for _, key := range []string{"items", "count", "limit", "locations"} {
			if _, ok := document[key]; !ok {
				t.Errorf("%s: collection has no %q", resource.path, key)
			}
		}
		// The web client indexes the registry by id and follows parentId to build the
		// tree, so those names are part of what it reads.
		registry, ok := document["locations"].([]any)
		if !ok || len(registry) == 0 {
			t.Fatalf("%s: locations = %v, want the registry", resource.path, document["locations"])
		}
		place, ok := registry[0].(map[string]any)
		if !ok {
			t.Fatalf("%s: location is not an object: %v", resource.path, registry[0])
		}
		for _, key := range []string{"id", "kind", "label", "parentId"} {
			if _, ok := place[key]; !ok {
				t.Errorf("%s: location has no %q, which the web client reads", resource.path, key)
			}
		}
		items, ok := document["items"].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("%s: items = %v, want one item", resource.path, document["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			t.Fatalf("%s: item is not an object: %v", resource.path, items[0])
		}
		for _, key := range resource.itemKeys {
			if _, ok := item[key]; !ok {
				t.Errorf("%s: item has no %q, which the web client reads", resource.path, key)
			}
		}
		if resource.nestedKey == "" {
			continue
		}
		nested, ok := item[resource.nestedKey].(map[string]any)
		if !ok {
			t.Fatalf("%s: %q is not an object", resource.path, resource.nestedKey)
		}
		for _, key := range resource.nested {
			if _, ok := nested[key]; !ok {
				t.Errorf("%s: %s has no %q", resource.path, resource.nestedKey, key)
			}
		}
	}

	// The places resource is read by the hub's own settings page, which lists where
	// the hub may work and registers one more.
	places := request(t, server, http.MethodGet, BasePath+"/places", nil)
	var registry map[string]any
	decodeResponse(t, places, &registry)
	for _, key := range []string{"items", "count", "limit", "locations"} {
		if _, ok := registry[key]; !ok {
			t.Errorf("/places: collection has no %q", key)
		}
	}
	registered := registerPlace(t, server, "/srv/repos/pricing")
	var place map[string]any
	decodeResponse(t, registered, &place)
	for _, key := range []string{"locationId", "registeredAt", "locations"} {
		if _, ok := place[key]; !ok {
			t.Errorf("/places: the registered place has no %q, which the web client reads", key)
		}
	}

	// The up-next entries the rail lists carry their own identity and status.
	response := request(t, server, http.MethodGet, BasePath+"/fleets", nil)
	var document struct {
		Items []struct {
			UpNext []map[string]any `json:"upNext"`
		} `json:"items"`
	}
	decodeResponse(t, response, &document)
	if len(document.Items) != 1 || len(document.Items[0].UpNext) == 0 {
		t.Fatalf("upNext = %v, want the nodes that have not started", document.Items)
	}
	for _, key := range []string{"id", "label", "status"} {
		if _, ok := document.Items[0].UpNext[0][key]; !ok {
			t.Errorf("up-next node has no %q, which the web client reads", key)
		}
	}
}

// The location contract: every response that carries work publishes a flat registry
// of the places that work runs in, and every item references one of them by an
// opaque server-issued id. The tests below are the published contract, not the
// implementation: they read the payload a consumer reads.

// mustDirectory builds a directory place, failing the test if the fact is invalid.
func mustDirectory(t *testing.T, directory string, parent *agenthub.Location) agenthub.Location {
	t.Helper()
	location, err := agenthub.NewDirectoryLocation(directory, parent)
	if err != nil {
		t.Fatalf("NewDirectoryLocation(%q): %v", directory, err)
	}
	return location
}

// registryDocument is the registry as a consumer decodes it.
type registryDocument struct {
	Locations []locationResource `json:"locations"`
}

// locationIndex maps a decoded registry by id, and reports each id's position, which
// is what makes "parents first" and "each place once" assertable.
func locationIndex(t *testing.T, locations []locationResource) map[string]int {
	t.Helper()
	index := map[string]int{}
	for position, location := range locations {
		if _, duplicate := index[location.ID]; duplicate {
			t.Errorf("the registry publishes %q twice", location.ID)
		}
		index[location.ID] = position
	}
	return index
}

func TestEveryWorkCollectionPublishesTheRegistryAndAResolvableReference(t *testing.T) {
	view := &viewStub{
		fleets:    []agenthub.Fleet{{ID: "fleet-1", Goal: "Expose pricing", Status: agenthub.StatusInProgress}},
		runs:      []agenthub.Run{{ID: "run-1", Label: "Daily digest", Status: agenthub.StatusDone}},
		schedules: []agenthub.Schedule{{ID: "schedule-1", Label: "Post digest"}},
	}
	server := newTestServer(t, view)

	for _, path := range []string{"/fleets", "/runs", "/schedules"} {
		response := request(t, server, http.MethodGet, BasePath+path, nil)
		var document struct {
			Items     []map[string]any   `json:"items"`
			Locations []locationResource `json:"locations"`
		}
		decodeResponse(t, response, &document)

		if len(document.Locations) != 1 {
			t.Fatalf("%s: locations = %+v, want exactly the unknown place", path, document.Locations)
		}
		unknown := document.Locations[0]
		if unknown.ID != "unknown" || unknown.Kind != agenthub.LocationUnknown || unknown.Label == "" {
			t.Errorf("%s: unknown place = %+v, want an identified, labelled place", path, unknown)
		}
		if unknown.ParentID != nil || unknown.Directory != "" || unknown.Ref != "" {
			t.Errorf("%s: the unknown place carries fields of another variant: %+v", path, unknown)
		}
		if len(document.Items) != 1 {
			t.Fatalf("%s: items = %+v, want one", path, document.Items)
		}
		if document.Items[0]["locationId"] != "unknown" {
			t.Errorf("%s: item locationId = %v, want the unknown place", path, document.Items[0]["locationId"])
		}
	}
}

func TestAWorkCollectionWithNoItemsStillPublishesTheRegistryItsSchemaRequires(t *testing.T) {
	// locations is required on all three located collections, so it must be present on
	// an empty page too: a consumer that resolves a reference against the registry must
	// never have to tell "no places" apart from "no field".
	server := newTestServer(t, &viewStub{})

	for _, path := range []string{"/fleets", "/runs", "/schedules"} {
		response := request(t, server, http.MethodGet, BasePath+path, nil)
		var document map[string]json.RawMessage
		decodeResponse(t, response, &document)

		raw, present := document["locations"]
		if !present {
			t.Fatalf("%s: an empty page dropped the locations its schema requires", path)
		}
		var locations []locationResource
		if err := json.Unmarshal(raw, &locations); err != nil {
			t.Fatalf("%s: locations is not a registry: %v", path, err)
		}
		if len(locations) != 1 || locations[0].ID != "unknown" {
			t.Errorf("%s: locations = %+v, want exactly the unknown place", path, locations)
		}
	}
}

func TestAnItemResourceCarriesItsOwnRegistryBecauseItHasNoEnvelope(t *testing.T) {
	view := &viewStub{
		fleets: []agenthub.Fleet{{ID: "fleet-1", Nodes: []agenthub.FleetNode{{ID: "api", Status: agenthub.StatusTodo}}}},
		runs:   []agenthub.Run{{ID: "run-1"}},
	}
	server := newTestServer(t, view)

	for path, itemID := range map[string]string{"/fleets/fleet-1": "fleet-1", "/runs/run-1": "run-1"} {
		response := request(t, server, http.MethodGet, BasePath+path, nil)
		var document struct {
			ID        string             `json:"id"`
			Locations []locationResource `json:"locations"`
			Nodes     []struct {
				LocationID string `json:"locationId"`
			} `json:"nodes"`
			LocationID string `json:"locationId"`
		}
		decodeResponse(t, response, &document)
		if document.ID != itemID || document.LocationID != "unknown" {
			t.Errorf("%s = %+v, want the item referencing the unknown place", path, document)
		}
		if len(document.Locations) == 0 {
			t.Errorf("%s carries no registry for its own reference", path)
		}
		for _, node := range document.Nodes {
			if node.LocationID != "unknown" {
				t.Errorf("%s: node locationId = %q, want the unknown place", path, node.LocationID)
			}
		}
	}
}

func TestTheRegistryIsClosedOverAncestryAndOrderedParentsFirst(t *testing.T) {
	repository := mustDirectory(t, "/srv/repos/pricing", nil)
	worktree := mustDirectory(t, "/srv/work/pricing", &repository)
	other := mustDirectory(t, "/srv/work/billing", &repository)
	view := &viewStub{runs: []agenthub.Run{
		{ID: "run-1", Location: worktree},
		{ID: "run-2", Location: other},
		{ID: "run-3", Location: worktree},
		{ID: "run-4"},
	}}

	response := request(t, newTestServer(t, view), http.MethodGet, BasePath+"/runs", nil)
	var document struct {
		Items []struct {
			ID         string `json:"id"`
			LocationID string `json:"locationId"`
		} `json:"items"`
		Locations []locationResource `json:"locations"`
	}
	decodeResponse(t, response, &document)

	index := locationIndex(t, document.Locations)
	// The repository is never referenced by an item, and must still be published: a
	// client cannot draw a place inside a parent it was never told about.
	if len(index) != 4 {
		t.Fatalf("registry = %+v, want unknown, the repository and the two worktrees", document.Locations)
	}
	if _, ok := index[repository.ID()]; !ok {
		t.Fatalf("the registry is not closed over ancestry: %+v", document.Locations)
	}
	for _, location := range document.Locations {
		if location.ParentID == nil {
			continue
		}
		if index[*location.ParentID] >= index[location.ID] {
			t.Errorf("%q is published before its parent %q", location.ID, *location.ParentID)
		}
	}
	for _, item := range document.Items {
		if _, resolves := index[item.LocationID]; !resolves {
			t.Errorf("%s references %q, which the registry does not contain", item.ID, item.LocationID)
		}
	}
	if document.Items[3].LocationID != "unknown" {
		t.Errorf("an item with no recorded place references %q, want the unknown place", document.Items[3].LocationID)
	}
}

func TestAFleetNodeRunsInItsOwnPlaceAndThatPlaceResolvesThroughBothReads(t *testing.T) {
	// A node genuinely differs from its fleet: it develops in a worktree of its own.
	// That is the one reference this contract adds whose place is not the fleet's, and
	// it has to resolve on both paths — against the collection's envelope registry and
	// against the single fleet's own.
	repository := mustDirectory(t, "/srv/repos/pricing", nil)
	worktree := mustDirectory(t, "/srv/work/pricing-api", &repository)
	started := mustDirectory(t, "/srv/work/pricing-web", &repository)
	view := &viewStub{fleets: []agenthub.Fleet{{
		ID: "fleet-1", Goal: "Expose pricing", Location: repository,
		Nodes: []agenthub.FleetNode{
			// The started node is not up next, so it is only reachable through Nodes.
			{ID: "web", Status: agenthub.StatusInProgress, Location: started},
			{ID: "api", Status: agenthub.StatusTodo, Location: worktree},
		},
	}}}
	server := newTestServer(t, view)

	type fleetDocument struct {
		LocationID string `json:"locationId"`
		Nodes      []struct {
			ID         string `json:"id"`
			LocationID string `json:"locationId"`
		} `json:"nodes"`
		UpNext []struct {
			ID         string `json:"id"`
			LocationID string `json:"locationId"`
		} `json:"upNext"`
	}

	// The collection carries no graph, so only the fleet's place and its up-next nodes'
	// places are referenced — and each must resolve against the envelope's registry.
	var collection struct {
		Items     []fleetDocument    `json:"items"`
		Locations []locationResource `json:"locations"`
	}
	decodeResponse(t, request(t, server, http.MethodGet, BasePath+"/fleets", nil), &collection)
	if len(collection.Items) != 1 || len(collection.Items[0].UpNext) != 1 {
		t.Fatalf("collection = %+v, want one fleet with one node up next", collection.Items)
	}
	collected := locationIndex(t, collection.Locations)
	if _, resolves := collected[collection.Items[0].UpNext[0].LocationID]; !resolves {
		t.Errorf("the collection's up-next node references %q, which its registry does not contain: %+v",
			collection.Items[0].UpNext[0].LocationID, collection.Locations)
	}
	if collection.Items[0].UpNext[0].LocationID != worktree.ID() {
		t.Errorf("up-next node locationId = %q, want its own worktree %q",
			collection.Items[0].UpNext[0].LocationID, worktree.ID())
	}

	// The single fleet carries its whole graph and its own registry. Its up-next nodes
	// are a subset of its nodes, so walking the graph is enough — which is only true
	// while up next is derived from Nodes, and this asserts it.
	var single struct {
		fleetDocument
		Locations []locationResource `json:"locations"`
	}
	decodeResponse(t, request(t, server, http.MethodGet, BasePath+"/fleets/fleet-1", nil), &single)
	index := locationIndex(t, single.Locations)
	if single.LocationID != repository.ID() {
		t.Errorf("fleet locationId = %q, want its repository %q", single.LocationID, repository.ID())
	}
	if len(single.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want the whole graph", single.Nodes)
	}
	for _, node := range append(single.Nodes, single.UpNext...) {
		if node.LocationID == "unknown" {
			t.Errorf("node %s references the unknown place, want the worktree it was recorded in", node.ID)
		}
		if _, resolves := index[node.LocationID]; !resolves {
			t.Errorf("node %s references %q, which the fleet's registry does not contain: %+v",
				node.ID, node.LocationID, single.Locations)
		}
	}
	// And the registry closes over what a node hangs under, so a client can draw the
	// worktree inside the repository without a second read.
	if _, ok := index[repository.ID()]; !ok {
		t.Fatalf("the fleet's registry is not closed over its nodes' ancestry: %+v", single.Locations)
	}
	for _, location := range single.Locations {
		if location.ParentID != nil && index[*location.ParentID] >= index[location.ID] {
			t.Errorf("%q is published before its parent %q", location.ID, *location.ParentID)
		}
	}
}

func TestALabelAndTheNaturalKeyComeFromTheServer(t *testing.T) {
	repository := mustDirectory(t, "/srv/repos/pricing", nil)
	remote, err := agenthub.NewRemoteLocation("github.com/acme/pricing", nil)
	if err != nil {
		t.Fatalf("NewRemoteLocation: %v", err)
	}
	view := &viewStub{runs: []agenthub.Run{{ID: "run-1", Location: repository}, {ID: "run-2", Location: remote}}}

	response := request(t, newTestServer(t, view), http.MethodGet, BasePath+"/runs", nil)
	var document registryDocument
	decodeResponse(t, response, &document)

	byID := map[string]locationResource{}
	for _, location := range document.Locations {
		byID[location.ID] = location
	}
	directory := byID[repository.ID()]
	if directory.Label != "pricing" || directory.Directory != "/srv/repos/pricing" || directory.Ref != "" {
		t.Errorf("directory place = %+v, want a server-computed label, its path, and no ref", directory)
	}
	published := byID[remote.ID()]
	if published.Ref != "github.com/acme/pricing" || published.Directory != "" {
		t.Errorf("remote place = %+v, want a ref and no directory", published)
	}
	if strings.Contains(directory.ID, "pricing") {
		t.Errorf("the id %q leaks the path it identifies", directory.ID)
	}
}

func TestTheRegistrySerialisesDeterministicallyAndKeepsTheEntityTagStable(t *testing.T) {
	repository := mustDirectory(t, "/srv/repos/pricing", nil)
	view := &viewStub{runs: []agenthub.Run{
		{ID: "run-1", Location: mustDirectory(t, "/srv/work/a", &repository)},
		{ID: "run-2", Location: mustDirectory(t, "/srv/work/b", &repository)},
		{ID: "run-3", Location: mustDirectory(t, "/srv/work/c", &repository)},
	}}
	server := newTestServer(t, view)

	first := request(t, server, http.MethodGet, BasePath+"/runs", nil)
	for range 20 {
		again := request(t, server, http.MethodGet, BasePath+"/runs", nil)
		if again.Body.String() != first.Body.String() {
			t.Fatalf("an unchanged read serialised differently:\n%s\n%s", first.Body.String(), again.Body.String())
		}
		if again.Header().Get("ETag") != first.Header().Get("ETag") {
			t.Fatalf("entity tag moved for an unchanged read: %q then %q",
				first.Header().Get("ETag"), again.Header().Get("ETag"))
		}
	}

	conditional := newRequest(http.MethodGet, BasePath+"/runs", nil)
	conditional.Header.Set("If-None-Match", first.Header().Get("ETag"))
	revalidated := httptest.NewRecorder()
	server.ServeHTTP(revalidated, conditional)
	if revalidated.Code != http.StatusNotModified {
		t.Fatalf("conditional read = %d, want 304", revalidated.Code)
	}
}

func TestThePagedActiveWorkContractGainsAFieldAndLosesNone(t *testing.T) {
	worktree := mustDirectory(t, "/srv/work/pricing", nil)
	view := &viewStub{runs: []agenthub.Run{{
		ID: "run-1", Running: true, Status: agenthub.StatusInProgress, Location: worktree,
	}}}
	server := newTestServer(t, view)

	response := request(t, server, http.MethodGet, BasePath+"/active-work", nil)
	var document struct {
		Items     []map[string]any   `json:"items"`
		Count     int                `json:"count"`
		Limit     int                `json:"limit"`
		Next      *string            `json:"next"`
		Locations []locationResource `json:"locations"`
	}
	decodeResponse(t, response, &document)
	if len(document.Items) != 1 {
		t.Fatalf("items = %+v, want one", document.Items)
	}
	// Every field the existing consumers read is still there and still means the same.
	for _, key := range []string{"id", "type", "status", "running"} {
		if _, ok := document.Items[0][key]; !ok {
			t.Errorf("the paged model lost %q", key)
		}
	}
	if document.Items[0]["locationId"] != worktree.ID() {
		t.Errorf("paged locationId = %v, want %q", document.Items[0]["locationId"], worktree.ID())
	}
	if _, resolves := locationIndex(t, document.Locations)[worktree.ID()]; !resolves {
		t.Errorf("the paged registry = %+v, does not resolve the item's reference", document.Locations)
	}

	// And the contract says the addition is optional, so a client that ignores it, or
	// a server that has nothing to say, both stay valid.
	activeWork := server.spec.schemas["ActiveWork"].(map[string]any)
	for _, required := range activeWork["required"].([]any) {
		if required == "locationId" {
			t.Error("locationId became required on the paged active-work model")
		}
	}
	collection := server.spec.schemas["ActiveWorkCollection"].(map[string]any)
	for _, required := range collection["required"].([]any) {
		if required == "locations" {
			t.Error("locations became required on the paged active-work collection")
		}
	}
}

func TestTheSpecificationDescribesTheLocationUnion(t *testing.T) {
	server := newTestServer(t, &viewStub{})

	kinds := schemaEnum(t, server.spec.schemas, "LocationKind")
	want := make([]string, 0, len(agenthub.LocationKinds()))
	for _, kind := range agenthub.LocationKinds() {
		want = append(want, string(kind))
	}
	assertSameStrings(t, kinds, want)

	location, ok := server.spec.schemas["Location"].(map[string]any)
	if !ok {
		t.Fatal("the specification has no Location schema")
	}
	variants, ok := location["oneOf"].([]any)
	if !ok || len(variants) != len(agenthub.LocationKinds()) {
		t.Fatalf("Location oneOf = %v, want one branch per variant", location["oneOf"])
	}
	discriminator, ok := location["discriminator"].(map[string]any)
	if !ok || discriminator["propertyName"] != "kind" {
		t.Fatalf("Location discriminator = %v, want a union discriminated on kind", location["discriminator"])
	}
	mapping, ok := discriminator["mapping"].(map[string]any)
	if !ok || len(mapping) != len(agenthub.LocationKinds()) {
		t.Fatalf("Location mapping = %v, want every variant mapped", discriminator["mapping"])
	}

	// A variant carries exactly its own fields: the schema refuses the others rather
	// than describing three nullable ones.
	unknown := server.spec.schemas["UnknownLocation"].(map[string]any)
	properties := unknown["properties"].(map[string]any)
	for _, forbidden := range []string{"directory", "ref"} {
		if _, present := properties[forbidden]; present {
			t.Errorf("the unknown variant describes %q", forbidden)
		}
	}
	if unknown["additionalProperties"] != false {
		t.Error("the unknown variant accepts fields of other variants")
	}
	directory := server.spec.schemas["DirectoryLocation"].(map[string]any)
	if _, present := directory["properties"].(map[string]any)["ref"]; present {
		t.Error("the directory variant describes a ref")
	}

	// The entry point publishes the vocabulary too, so a consumer discovers the
	// variants without reading the specification.
	description := request(t, server, http.MethodGet, BasePath, nil)
	var entry struct {
		Vocabularies struct {
			LocationKind []string `json:"locationKind"`
		} `json:"vocabularies"`
	}
	decodeResponse(t, description, &entry)
	assertSameStrings(t, entry.Vocabularies.LocationKind, want)
}

func TestOnlyADiscriminatorObjectIsRewrittenAsOne(t *testing.T) {
	// A discriminator is legal on a base schema whose variants compose with allOf, so a
	// nearby oneOf is not what makes one. A schema property that happens to be called
	// "discriminator" is not one, and rewriting it as a mapping would corrupt it.
	rewritten, ok := rewriteRefs(map[string]any{
		"discriminator": map[string]any{
			"propertyName": "kind",
			"mapping":      map[string]any{"directory": "#/components/schemas/DirectoryLocation"},
		},
		"properties": map[string]any{
			"discriminator": map[string]any{"type": "string"},
		},
	}).(map[string]any)
	if !ok {
		t.Fatal("rewriteRefs did not answer with a schema")
	}

	discriminator := rewritten["discriminator"].(map[string]any)
	mapping := discriminator["mapping"].(map[string]any)
	if mapping["directory"] != "#/$defs/DirectoryLocation" {
		t.Errorf("mapping = %v, want a reference the standalone document contains", mapping)
	}
	property := rewritten["properties"].(map[string]any)["discriminator"].(map[string]any)
	if property["type"] != "string" {
		t.Errorf("a property called \"discriminator\" was rewritten as one: %v", property)
	}
}

func TestTheRegistryIsPublishedAsAModelOfItsOwn(t *testing.T) {
	server := newTestServer(t, &viewStub{})

	response := request(t, server, http.MethodGet, BasePath+"/schemas/"+modelLocation, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("location schema = %d: %s", response.Code, response.Body.String())
	}
	var document map[string]any
	decodeResponse(t, response, &document)
	if document["oneOf"] == nil || document["$defs"] == nil {
		t.Fatalf("the published union is not a standalone schema: %v", sortedKeys(document))
	}
	encoded, _ := json.Marshal(document)
	if bytes.Contains(encoded, []byte("#/components/schemas/")) {
		t.Error("the union's discriminator still points into the OpenAPI document")
	}
}

// settingsStub answers the settings read with what the catalogue ships, which is
// what an installation that has configured nothing resolves to.
type settingsStub struct {
	err error
}

func (s settingsStub) Settings(context.Context) (setting.Resolution, error) {
	if s.err != nil {
		return nil, s.err
	}
	resolution := make(setting.Resolution, 0, len(setting.Specs()))
	for _, spec := range setting.Specs() {
		resolution = append(resolution, setting.Value{
			Key: spec.Key, Enabled: spec.Factory, Scope: setting.FactoryScope, Version: 1,
		})
	}
	return resolution, nil
}

// The configuration surface reads what the tool is set to do, and — as importantly —
// where each answer came from, so it can say "inherited from the installation"
// without deriving inheritance itself.
func TestSettingsArePublishedWithTheScopeEachValueCameFrom(t *testing.T) {
	server := newTestServer(t, &viewStub{}, func(options *Options) {
		options.Settings = fixedSettings{setting.Resolution{{
			Key:     setting.KeySteeringEnabled,
			Enabled: true,
			Scope:   setting.GlobalScope,
			Version: 4,
		}}}
	})

	response := request(t, server, http.MethodGet, BasePath+"/settings", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var document struct {
		Items []struct {
			Key     string `json:"key"`
			Purpose string `json:"purpose"`
			Enabled bool   `json:"enabled"`
			Source  string `json:"source"`
			Version int    `json:"version"`
		} `json:"items"`
	}
	decodeResponse(t, response, &document)
	if len(document.Items) != 1 {
		t.Fatalf("published %d setting(s), want the one that was resolved", len(document.Items))
	}
	published := document.Items[0]
	if published.Key != string(setting.KeySteeringEnabled) || !published.Enabled {
		t.Fatalf("published %+v, want the resolved value", published)
	}
	if published.Source != "global" || published.Version != 4 {
		t.Fatalf("published source %q v%d, want the installation's version 4", published.Source, published.Version)
	}
	if published.Purpose == "" {
		t.Fatal("a setting is published without saying what it decides")
	}
}

// A scope names an absolute path on the server. What a consumer needs is which kind
// of scope answered, so the path never leaves the machine it describes.
func TestAPlacesSettingIsPublishedAsAPlaceAndNotAsAPath(t *testing.T) {
	server := newTestServer(t, &viewStub{}, func(options *Options) {
		options.Settings = fixedSettings{setting.Resolution{{
			Key:   setting.KeySteeringEnabled,
			Scope: setting.DirectoryScope("/src/agents"),
		}}}
	})

	response := request(t, server, http.MethodGet, BasePath+"/settings", nil)

	if body := response.Body.String(); !strings.Contains(body, `"source":"directory"`) {
		t.Fatalf("the source is not published as a kind: %s", body)
	} else if strings.Contains(body, "/src/agents") {
		t.Fatalf("the server's directory layout was published: %s", body)
	}
}

// A configuration store that cannot answer is a dependency failure, reported as one
// rather than as an empty configuration: an operator reading "nothing is configured"
// would draw exactly the wrong conclusion.
func TestASettingsStoreThatCannotAnswerIsADependencyFailure(t *testing.T) {
	server := newTestServer(t, &viewStub{}, func(options *Options) {
		options.Settings = settingsStub{err: agenthub.ErrUnavailable}
	})

	response := request(t, server, http.MethodGet, BasePath+"/settings", nil)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

// fixedSettings answers with exactly what a test states.
type fixedSettings struct {
	resolution setting.Resolution
}

func (f fixedSettings) Settings(context.Context) (setting.Resolution, error) {
	return f.resolution, nil
}

// placesStub is a stateful stand-in for the place registry: it applies a
// registration the way the core does — one place per directory, keeping the first
// registration — so the transport's tests assert on resources rather than on calls.
type placesStub struct {
	places []agenthub.RegisteredPlace
	// missing and unversioned are the directories that stand in for the two machine
	// refusals.
	missing     []string
	unversioned []string
	err         error
}

func (p *placesStub) RegisteredPlaces(context.Context) ([]agenthub.RegisteredPlace, error) {
	return p.places, p.err
}

func (p *placesStub) RegisterPlace(_ context.Context, directory, by string) (agenthub.RegisteredPlace, error) {
	if p.err != nil {
		return agenthub.RegisteredPlace{}, p.err
	}
	if err := agenthub.ValidatePlaceDirectory(directory); err != nil {
		return agenthub.RegisteredPlace{}, err
	}
	if slices.Contains(p.missing, directory) {
		return agenthub.RegisteredPlace{}, fmt.Errorf("%w: %s", agenthub.ErrNoSuchDirectory, directory)
	}
	if slices.Contains(p.unversioned, directory) {
		return agenthub.RegisteredPlace{}, fmt.Errorf("%w: %s", agenthub.ErrNotARepository, directory)
	}
	location, err := agenthub.RecordedPlace{Directory: directory}.Location()
	if err != nil {
		return agenthub.RegisteredPlace{}, err
	}
	for _, existing := range p.places {
		if existing.Location.ID() == location.ID() {
			return existing, nil
		}
	}
	place := agenthub.RegisteredPlace{Location: location, RegisteredAt: fixedNow, RegisteredBy: by}
	p.places = append(p.places, place)
	return place, nil
}

// starterStub stands in for the start surface. The rules a start obeys — one run
// per request identity, no second loop in one working tree — are the core's, and are
// asserted there; what the transport owns is the request it builds, the resource it
// answers with and the problem it reports, so the stub records and replays.
type starterStub struct {
	requests []agenthub.StartRequest
	started  agenthub.StartedWork
	err      error
}

func (s *starterStub) StartWork(_ context.Context, request agenthub.StartRequest) (agenthub.StartedWork, error) {
	s.requests = append(s.requests, request)
	if s.err != nil {
		return agenthub.StartedWork{}, s.err
	}
	started := s.started
	if started.RunID == "" {
		place, err := agenthub.RecordedPlace{Directory: "/srv/repos/pricing"}.Location()
		if err != nil {
			return agenthub.StartedWork{}, err
		}
		started = agenthub.StartedWork{
			RunID:     "develop-1",
			Kind:      request.Kind,
			Location:  place,
			Prompt:    request.Prompt,
			StartedAt: fixedNow,
			StartedBy: request.StartedBy,
		}
	}
	return started, nil
}
