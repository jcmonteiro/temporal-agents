package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"temporal-agents/internal/identity"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/promptconfig"
)

type promptConfigurationStub struct {
	catalogue  instruction.Catalogue
	locationID string
	key        instruction.Key
	text       string
	savedBy    string
	setErr     error
	resetErr   error
	readErr    error
}

func (p *promptConfigurationStub) Catalogue(context.Context, string) (instruction.Catalogue, error) {
	return p.catalogue, p.readErr
}

func (p *promptConfigurationStub) Set(_ context.Context, locationID string, key instruction.Key, text, savedBy string) (instruction.Record, error) {
	p.locationID, p.key, p.text, p.savedBy = locationID, key, text, savedBy
	return instruction.Record{}, p.setErr
}

func (p *promptConfigurationStub) Reset(_ context.Context, locationID string, key instruction.Key) error {
	p.locationID, p.key = locationID, key
	return p.resetErr
}

func promptCatalogue(t *testing.T) instruction.Catalogue {
	t.Helper()
	spec, ok := instruction.SpecFor(instruction.KeyReviewImplement)
	if !ok {
		t.Fatal("review implement instruction is not governed")
	}
	return instruction.Catalogue{{
		Spec: spec,
		Effective: instruction.Value{
			Key: instruction.KeyReviewImplement, Text: "review here {{.Review}}",
			Scope: instruction.DirectoryScope("/src/agents"), Version: 3,
		},
		Inherited: instruction.Value{
			Key: instruction.KeyReviewImplement, Text: "review everywhere {{.Review}}",
			Scope: instruction.GlobalScope, Version: 2,
		},
		Overridden: true,
	}}
}

func TestPromptCataloguePublishesEditableAndInheritedValuesWithSafetyMetadata(t *testing.T) {
	prompts := &promptConfigurationStub{catalogue: promptCatalogue(t)}
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.Prompts = prompts })

	response := request(t, server, http.MethodGet, BasePath+"/prompts?locationId=place-1", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var document struct {
		Items []struct {
			Key           string `json:"key"`
			Purpose       string `json:"purpose"`
			Effective     string `json:"effective"`
			Inherited     string `json:"inherited"`
			Source        string `json:"source"`
			InheritedFrom string `json:"inheritedFrom"`
			Overridden    bool   `json:"overridden"`
			SystemBlock   string `json:"systemBlock"`
			Advanced      bool   `json:"advanced"`
			Required      []struct {
				Name    string `json:"name"`
				Action  string `json:"action"`
				Purpose string `json:"purpose"`
			} `json:"requiredInserts"`
		} `json:"items"`
	}
	decodeResponse(t, response, &document)
	if len(document.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(document.Items))
	}
	item := document.Items[0]
	if item.Key != string(instruction.KeyReviewImplement) || item.Purpose == "" {
		t.Fatalf("identity metadata = %+v", item)
	}
	if item.Effective != "review here {{.Review}}" || item.Inherited != "review everywhere {{.Review}}" {
		t.Fatalf("values = %+v", item)
	}
	if item.Source != "directory" || item.InheritedFrom != "global" || !item.Overridden {
		t.Fatalf("inheritance = %+v", item)
	}
	if len(item.Required) != 1 || item.Required[0].Action != "{{.Review}}" || item.Required[0].Purpose == "" {
		t.Fatalf("required inserts = %+v", item.Required)
	}
}

func TestPromptResourcesKeepTheFieldNamesTheWebClientReads(t *testing.T) {
	prompts := &promptConfigurationStub{catalogue: promptCatalogue(t)}
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.Prompts = prompts })
	response := request(t, server, http.MethodGet, BasePath+"/prompts", nil)
	var document map[string]any
	decodeResponse(t, response, &document)
	for _, key := range []string{"items", "count", "limit"} {
		if _, ok := document[key]; !ok {
			t.Errorf("prompt collection has no %q", key)
		}
	}
	items, ok := document["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %v, want one prompt", document["items"])
	}
	item := items[0].(map[string]any)
	for _, key := range []string{
		"key", "purpose", "effective", "inherited", "source", "inheritedFrom",
		"version", "inheritedVersion", "overridden", "systemBlock",
		"requiredInserts", "advanced", "maxLength",
	} {
		if _, ok := item[key]; !ok {
			t.Errorf("prompt has no %q, which the web client reads", key)
		}
	}
}

func TestSavingAPromptAttributesTheAuthenticatedPrincipalAndPlace(t *testing.T) {
	prompts := &promptConfigurationStub{}
	server := newTestServer(t, &viewStub{}, func(options *Options) {
		options.AllowUnauthenticated = false
		options.AuthToken = "correct horse battery staple"
		options.Prompts = prompts
	})
	body := `{"text":"Review this repository thoroughly"}`
	req := newRequest(http.MethodPut, BasePath+"/prompts/review.perform?locationId=place-1", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer correct horse battery staple")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, req)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if prompts.locationID != "place-1" || prompts.key != instruction.KeyReviewPerform || prompts.text != "Review this repository thoroughly" {
		t.Fatalf("saved location=%q key=%q text=%q", prompts.locationID, prompts.key, prompts.text)
	}
	wantPrincipal := identity.LocalIssuer + "|" + identity.StaticTokenSubject
	if prompts.savedBy != wantPrincipal {
		t.Fatalf("savedBy = %q, want %q", prompts.savedBy, wantPrincipal)
	}
}

func TestResettingAPromptUsesTheSameScopeSelection(t *testing.T) {
	prompts := &promptConfigurationStub{}
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.Prompts = prompts })
	req := newRequest(http.MethodDelete, BasePath+"/prompts/review.perform?locationId=place-1", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, req)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if prompts.locationID != "place-1" || prompts.key != instruction.KeyReviewPerform {
		t.Fatalf("reset location=%q key=%q", prompts.locationID, prompts.key)
	}
}

func TestAnInvalidPromptIsAProblemThatNamesTheCause(t *testing.T) {
	prompts := &promptConfigurationStub{setErr: errors.New("wrapper: " + instruction.ErrInvalidText.Error() + ": must insert {{.Review}}")}
	prompts.setErr = errors.Join(instruction.ErrInvalidText, prompts.setErr)
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.Prompts = prompts })
	req := newRequest(http.MethodPut, BasePath+"/prompts/review.implement", strings.NewReader(`{"text":"fix it"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()

	server.ServeHTTP(response, req)

	requireProblem(t, response, http.StatusUnprocessableEntity, codeInvalidPrompt)
	if !strings.Contains(response.Body.String(), "{{.Review}}") {
		t.Fatalf("problem does not name the missing insert: %s", response.Body.String())
	}
}

func TestAnUnknownPromptPlaceIsNotFound(t *testing.T) {
	prompts := &promptConfigurationStub{readErr: promptconfig.ErrPlaceNotFound}
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.Prompts = prompts })

	response := request(t, server, http.MethodGet, BasePath+"/prompts?locationId=missing", nil)

	requireProblem(t, response, http.StatusNotFound, codeNotFound)
}
