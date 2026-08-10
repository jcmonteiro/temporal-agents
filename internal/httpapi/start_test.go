package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/identity"
)

// Starting work over HTTP: what the contract lets a caller ask for, what the
// response says about work that is not observable yet, and how a refusal reads.

// startWork posts a start the way the hub's own page does.
func startWork(t *testing.T, server *Server, body string, prepare ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	request := newRequest(http.MethodPost, BasePath+"/runs", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, change := range prepare {
		change(request)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

// aStartOf is the body of a well-formed develop start.
func aStartOf(placeID string) string {
	body, _ := json.Marshal(startWorkRequest{
		RequestID: "request-1", Kind: agenthub.StartDevelop,
		PlaceID: placeID, Prompt: "make the flaky test pass",
	})
	return string(body)
}

func TestStartedWorkIsAnsweredWithWhereToFindItAndNoInventedStatus(t *testing.T) {
	starter := &starterStub{}
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.Start = starter })

	created := startWork(t, server, aStartOf("dir-1"))

	if created.Code != http.StatusCreated {
		t.Fatalf("start = %d: %s", created.Code, created.Body.String())
	}
	if got := created.Header().Get("Location"); got != BasePath+"/runs/develop-1" {
		t.Errorf("Location = %q, want the run resource", got)
	}
	var started map[string]any
	decodeResponse(t, created, &started)
	for _, key := range []string{"id", "kind", "type", "label", "locationId", "startedAt", "locations"} {
		if _, ok := started[key]; !ok {
			t.Errorf("started work has no %q", key)
		}
	}
	// The work has been submitted, not observed: it has no status, no iterations and
	// no token usage yet, and inventing any of them would be read as a fact.
	for _, key := range []string{"status", "iterations", "tokens", "dismissible", "endedAt"} {
		if _, ok := started[key]; ok {
			t.Errorf("started work carries %q, which is not a fact yet", key)
		}
	}
	if started["type"] != string(agenthub.RunTypeDevelop) || started["kind"] != string(agenthub.KindRun) {
		t.Errorf("started work = %v, want a develop run", started)
	}
	// The place travels with its registry, so the reference resolves without a
	// second read.
	locations, ok := started["locations"].([]any)
	if !ok || len(locations) == 0 {
		t.Fatalf("locations = %v, want the registry", started["locations"])
	}
}

func TestTheContractOffersNoWayToNameADirectory(t *testing.T) {
	server := newTestServer(t, &viewStub{})

	// The one thing a caller must never be able to do is point the hub at a
	// directory of its choosing. An unknown field is refused rather than ignored, so
	// a caller cannot smuggle one past the contract.
	refused := startWork(t, server, `{"requestId":"request-1","kind":"develop",`+
		`"directory":"/etc","prompt":"do the thing"}`)

	requireProblem(t, refused, http.StatusBadRequest, codeInvalidRequest)
	schema := request(t, server, http.MethodGet, BasePath+"/schemas/"+modelStartRequest, nil)
	var published struct {
		Properties map[string]any `json:"properties"`
	}
	decodeResponse(t, schema, &published)
	for name := range published.Properties {
		if strings.Contains(strings.ToLower(name), "dir") || strings.Contains(strings.ToLower(name), "path") {
			t.Errorf("the published start request has a %q field", name)
		}
	}
}

func TestAStartCarriesTheRequestIdentityAndThePrincipalToTheCore(t *testing.T) {
	starter := &starterStub{}
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator},
		func(options *Options) { options.Start = starter })
	session := signInThroughTheBrowser(t, server)

	created := startWork(t, server, aStartOf("dir-1"), func(request *http.Request) {
		request.AddCookie(session)
	})

	if created.Code != http.StatusCreated {
		t.Fatalf("start = %d: %s", created.Code, created.Body.String())
	}
	if len(starter.requests) != 1 {
		t.Fatalf("requests = %d, want one", len(starter.requests))
	}
	asked := starter.requests[0]
	if asked.RequestID != "request-1" || asked.Kind != agenthub.StartDevelop || asked.PlaceID != "dir-1" {
		t.Errorf("request = %+v, want what the body asked for", asked)
	}
	if asked.StartedBy != (identity.Principal{
		Issuer: theOperator.Issuer, Subject: theOperator.Subject,
	}).ID() {
		t.Errorf("startedBy = %q, want the principal who signed in", asked.StartedBy)
	}
}

func TestARefusedStartReadsAsWhatItIs(t *testing.T) {
	cases := map[string]struct {
		err      error
		status   int
		code     problemCode
		detail   string
		conflict string
	}{
		"work is already running there": {
			err:    agenthub.PlaceIsBusy{RunID: "develop-9", Place: "pricing"},
			status: http.StatusConflict, code: codePlaceIsBusy, detail: "develop-9",
			conflict: "develop-9",
		},
		"the place is not one the hub knows": {
			err:    fmt.Errorf("%w: this hub knows no place \"invented\" to work in", agenthub.ErrInvalid),
			status: http.StatusBadRequest, code: codeInvalidRequest, detail: "invented",
		},
		"the orchestrator cannot be reached": {
			err:    fmt.Errorf("%w: start the work", agenthub.ErrUnavailable),
			status: http.StatusServiceUnavailable, code: codeDependencyUnavailable,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := newTestServer(t, &viewStub{}, func(options *Options) {
				options.Start = &starterStub{err: tc.err}
			})

			problem := requireProblem(t, startWork(t, server, aStartOf("dir-1")), tc.status, tc.code)

			if tc.detail != "" && !strings.Contains(problem.Detail, tc.detail) {
				t.Errorf("detail = %q, want it to name %q", problem.Detail, tc.detail)
			}
			// A surface that offers to take the operator to the work in the way must
			// not have to read its identity out of a sentence.
			if problem.ConflictingRunID != tc.conflict {
				t.Errorf("conflictingRunId = %q, want %q", problem.ConflictingRunID, tc.conflict)
			}
		})
	}
}

func TestStartingWorkIsSubjectToTheRulesEveryMutationIs(t *testing.T) {
	server := newTestServer(t, &viewStub{})

	crossSite := startWork(t, server, aStartOf("dir-1"), func(request *http.Request) {
		request.Header.Set("Sec-Fetch-Site", "cross-site")
	})
	requireProblem(t, crossSite, http.StatusForbidden, codeCrossSiteRequest)

	wrongMedia := newRequest(http.MethodPost, BasePath+"/runs", strings.NewReader(aStartOf("dir-1")))
	wrongMedia.Header.Set("Content-Type", "text/plain")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, wrongMedia)
	requireProblem(t, recorder, http.StatusUnsupportedMediaType, codeUnsupportedMediaType)
}

func TestAnUnauthenticatedBrowserCannotStartWork(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})

	requireProblem(t, startWork(t, server, aStartOf("dir-1")),
		http.StatusUnauthorized, codeAuthenticationRequired)
}

func TestADeploymentThatOnlyWatchesOffersNoWayToStartWork(t *testing.T) {
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.Start = nil })

	refused := startWork(t, server, aStartOf("dir-1"))

	requireProblem(t, refused, http.StatusMethodNotAllowed, codeMethodNotAllowed)
	if listed := request(t, server, http.MethodGet, BasePath+"/runs", nil); listed.Code != http.StatusOK {
		t.Errorf("reading the runs = %d, want the read surface untouched", listed.Code)
	}
}

// TestTheStartSurfaceIsNotReachedThroughTheReadPort pins the seam: the work view is
// what reads, and it cannot start anything.
func TestTheStartSurfaceIsNotReachedThroughTheReadPort(t *testing.T) {
	var view WorkView = &viewStub{}

	if _, isStarter := view.(WorkStarter); isStarter {
		t.Error("the read port also starts work, which is the seam this API keeps")
	}
	if !errors.Is(agenthub.ErrPlaceIsBusy, agenthub.ErrPlaceIsBusy) {
		t.Fatal("unreachable")
	}
}
