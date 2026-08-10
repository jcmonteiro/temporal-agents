package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/identity"
)

// Where the hub may work, over HTTP: the registration a browser sends, the refusals
// it gets back, and the reads that make a place with no work in it visible at all.

// registerPlace posts a registration the way the hub's own page does.
func registerPlace(t *testing.T, server *Server, directory string, prepare ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(placeRegistrationRequest{Directory: directory})
	if err != nil {
		t.Fatalf("marshal the registration: %v", err)
	}
	request := newRequest(http.MethodPost, BasePath+"/places", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, change := range prepare {
		change(request)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func TestARegisteredPlaceIsPublishedWithTheRegistryItsReferenceResolvesAgainst(t *testing.T) {
	server := newTestServer(t, &viewStub{})

	created := registerPlace(t, server, "/srv/repos/pricing")

	if created.Code != http.StatusCreated {
		t.Fatalf("register = %d: %s", created.Code, created.Body.String())
	}
	var place placeResource
	decodeResponse(t, created, &place)
	if place.LocationID == "" || place.RegisteredAt == nil {
		t.Fatalf("place = %+v, want a referenced place and a time", place)
	}
	// The resource is served on its own, so it carries the registry itself: nothing
	// resolves a reference against a document it has to fetch first.
	if !containsLocation(place.Locations, place.LocationID) {
		t.Fatalf("locations = %+v, want the registered place", place.Locations)
	}

	listed := request(t, server, http.MethodGet, BasePath+"/places", nil)
	var document locatedCollection[placeResource]
	decodeResponse(t, listed, &document)
	if document.Count != 1 || document.Items[0].LocationID != place.LocationID {
		t.Fatalf("places = %+v, want the registered one", document)
	}
	if !containsLocation(document.Locations, place.LocationID) {
		t.Fatalf("locations = %+v, want the registered place", document.Locations)
	}
	for _, location := range document.Locations {
		if location.ID == place.LocationID && location.Label != "pricing" {
			t.Errorf("label = %q, want the server's own label", location.Label)
		}
	}
}

func TestRegisteringTheSamePlaceTwiceAddressesOneResource(t *testing.T) {
	server := newTestServer(t, &viewStub{})

	first := registerPlace(t, server, "/srv/repos/pricing")
	again := registerPlace(t, server, "/srv/repos/pricing")

	if again.Code != http.StatusCreated {
		t.Fatalf("repeat = %d: %s", again.Code, again.Body.String())
	}
	if first.Body.String() != again.Body.String() {
		t.Errorf("repeat described a different place:\n%s\n%s", first.Body.String(), again.Body.String())
	}
	listed := request(t, server, http.MethodGet, BasePath+"/places", nil)
	var document locatedCollection[placeResource]
	decodeResponse(t, listed, &document)
	if document.Count != 1 {
		t.Errorf("places = %d, want one", document.Count)
	}
}

func TestADirectoryTheHubCannotWorkInIsRefusedWithTheReason(t *testing.T) {
	places := &placesStub{missing: []string{"/srv/gone"}, unversioned: []string{"/srv/notes"}}
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.Places = places })

	cases := map[string]struct {
		directory string
		status    int
		code      problemCode
		detail    string
	}{
		"nothing is there": {
			directory: "/srv/gone", status: http.StatusUnprocessableEntity,
			code: codeNotAPlace, detail: "/srv/gone",
		},
		"no repository holds it": {
			directory: "/srv/notes", status: http.StatusUnprocessableEntity,
			code: codeNotAPlace, detail: "repository",
		},
		"the path is relative": {
			directory: "srv/repos/pricing", status: http.StatusBadRequest,
			code: codeInvalidRequest, detail: "absolute",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			response := registerPlace(t, server, tc.directory)

			problem := requireProblem(t, response, tc.status, tc.code)
			if !strings.Contains(problem.Detail, tc.detail) {
				t.Errorf("detail = %q, want it to name %q", problem.Detail, tc.detail)
			}
		})
	}
	listed := request(t, server, http.MethodGet, BasePath+"/places", nil)
	var document locatedCollection[placeResource]
	decodeResponse(t, listed, &document)
	if document.Count != 0 {
		t.Errorf("places = %+v, want none: a refused registration registers nothing", document.Items)
	}
}

func TestRegisteringAPlaceIsSubjectToTheRulesEveryMutationIs(t *testing.T) {
	server := newTestServer(t, &viewStub{})

	crossSite := registerPlace(t, server, "/srv/repos/pricing", func(request *http.Request) {
		request.Header.Set("Sec-Fetch-Site", "cross-site")
	})
	requireProblem(t, crossSite, http.StatusForbidden, codeCrossSiteRequest)

	wrongMedia := newRequest(http.MethodPost, BasePath+"/places",
		strings.NewReader(`{"directory":"/srv/repos/pricing"}`))
	wrongMedia.Header.Set("Content-Type", "text/plain")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, wrongMedia)
	requireProblem(t, recorder, http.StatusUnsupportedMediaType, codeUnsupportedMediaType)

	unknownField := newRequest(http.MethodPost, BasePath+"/places",
		strings.NewReader(`{"directory":"/srv/repos/pricing","parentId":"invented"}`))
	unknownField.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, unknownField)
	requireProblem(t, recorder, http.StatusBadRequest, codeInvalidRequest)
}

func TestARegistrationRecordsWhoAskedForIt(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})
	session := signInThroughTheBrowser(t, server)

	created := registerPlace(t, server, "/srv/repos/pricing", func(request *http.Request) {
		request.AddCookie(session)
	})

	if created.Code != http.StatusCreated {
		t.Fatalf("register = %d: %s", created.Code, created.Body.String())
	}
	var place placeResource
	decodeResponse(t, created, &place)
	if place.RegisteredBy != (identity.Principal{
		Issuer: theOperator.Issuer, Subject: theOperator.Subject,
	}).ID() {
		t.Errorf("registeredBy = %q, want the principal who signed in", place.RegisteredBy)
	}
}

func TestAnUnauthenticatedBrowserCannotRegisterAPlace(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})

	response := registerPlace(t, server, "/srv/repos/pricing")

	requireProblem(t, response, http.StatusUnauthorized, codeAuthenticationRequired)
}

func TestADeploymentWithNoRegistryServesNoPlaces(t *testing.T) {
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.Places = nil })

	requireProblem(t, request(t, server, http.MethodGet, BasePath+"/places", nil),
		http.StatusNotFound, codeNotFound)
}

func TestAPlaceRegistryThatCannotAnswerIsADependencyFailure(t *testing.T) {
	server := newTestServer(t, &viewStub{}, func(options *Options) {
		options.Places = &placesStub{err: agenthub.ErrUnavailable}
	})

	requireProblem(t, request(t, server, http.MethodGet, BasePath+"/places", nil),
		http.StatusServiceUnavailable, codeDependencyUnavailable)
	requireProblem(t, registerPlace(t, server, "/srv/repos/pricing"),
		http.StatusServiceUnavailable, codeDependencyUnavailable)
}

// containsLocation reports whether a registry publishes the referenced place.
func containsLocation(locations []locationResource, id string) bool {
	for _, location := range locations {
		if location.ID == id {
			return true
		}
	}
	return false
}
