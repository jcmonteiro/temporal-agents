package httpapi

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"temporal-agents/internal/agenthub"
)

// The contract is published, not described in prose: an OpenAPI document served over
// HTTP, and a standalone JSON Schema per model derived from that same document. Both
// come from one artefact, so the specification cannot drift from the schemas, and the
// package's tests check the document against the routing table and against the model's
// own enumerations — so it cannot drift from the implementation either.
//
// Schemas are reachable over HTTP on purpose: a consumer can validate a payload, and
// build its own tests and code generation, without this repository.

// specFS holds the OpenAPI document. Embedding it means the binary always serves the
// contract it was built with, with no file to deploy alongside it.
//
//go:embed spec/openapi.json
var specFS embed.FS

// specPath is where the document lives inside specFS.
const specPath = "spec/openapi.json"

// openAPIMediaType is the media type of an OpenAPI document in JSON.
const openAPIMediaType = "application/vnd.oai.openapi+json;version=3.1"

// schemaMediaType is the media type of a JSON Schema document.
const schemaMediaType = "application/schema+json"

// The published models. Each name carries its own version, so a future revision of a
// model can be served next to this one within the same API version instead of forcing
// every consumer onto a new API.
const (
	modelActiveWorkCollection = "active-work-collection.v1"
	modelFleet                = "fleet.v1"
	modelFleetCollection      = "fleet-collection.v1"
	modelRun                  = "run.v1"
	modelRunCollection        = "run-collection.v1"
	modelSchedule             = "schedule.v1"
	modelScheduleCollection   = "schedule-collection.v1"
	modelLocation             = "location.v1"
	modelDismissal            = "dismissal.v1"
	modelDismissalCollection  = "dismissal-collection.v1"
	modelDismissalRequest     = "dismissal-request.v1"
	modelProblem              = "problem.v1"
	modelServiceDescription   = "service-description.v1"
	modelHealth               = "health.v1"
)

// modelSchemas maps each published model name onto the schema in the specification
// that defines it. It is the single place the two vocabularies meet.
var modelSchemas = map[string]string{
	modelActiveWorkCollection: "ActiveWorkCollection",
	modelFleet:                "Fleet",
	modelFleetCollection:      "FleetCollection",
	modelRun:                  "Run",
	modelRunCollection:        "RunCollection",
	modelSchedule:             "Schedule",
	modelScheduleCollection:   "ScheduleCollection",
	modelLocation:             "Location",
	modelDismissal:            "Dismissal",
	modelDismissalCollection:  "DismissalCollection",
	modelDismissalRequest:     "DismissalRequest",
	modelProblem:              "Problem",
	modelServiceDescription:   "ServiceDescription",
	modelHealth:               "Health",
}

// modelNames lists the published models in a stable order.
func modelNames() []string {
	names := make([]string, 0, len(modelSchemas))
	for name := range modelSchemas {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// specification is the parsed OpenAPI document plus the bytes served for it.
type specification struct {
	// document is the whole document, kept as decoded JSON so schemas can be lifted
	// out of it without a second source of truth.
	document map[string]any
	// body is what the specification endpoint serves.
	body []byte
	// schemas are the document's component schemas by name.
	schemas map[string]any
}

// loadSpecification reads and validates the embedded document. A document that does
// not parse, or that is missing a schema a published model names, is a build-time
// defect: the server refuses to start rather than serve a contract it cannot back.
func loadSpecification() (specification, error) {
	body, err := specFS.ReadFile(specPath)
	if err != nil {
		return specification{}, fmt.Errorf("read the embedded API specification: %w", err)
	}
	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		return specification{}, fmt.Errorf("parse the embedded API specification: %w", err)
	}
	components, _ := document["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if len(schemas) == 0 {
		return specification{}, fmt.Errorf("the API specification defines no component schemas")
	}
	for model, name := range modelSchemas {
		if _, ok := schemas[name]; !ok {
			return specification{}, fmt.Errorf("the API specification has no schema %q for the model %q", name, model)
		}
	}
	return specification{document: document, body: body, schemas: schemas}, nil
}

// schemaURI is where a model's schema is served.
func (s *Server) schemaURI(model string) string {
	return s.basePath + "/schemas/" + model
}

// handleSpecification serves the OpenAPI document.
func (s *Server) handleSpecification(w http.ResponseWriter, r *http.Request) {
	s.writeStaticJSON(w, r, openAPIMediaType, s.spec.body)
}

// handleSchemaIndex lists the published models and where their schemas are, so a
// consumer can discover them by following links rather than by knowing names.
func (s *Server) handleSchemaIndex(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		// Model is the published model's name, including its version.
		Model string `json:"model"`
		// Href is where its schema is served.
		Href string `json:"href"`
	}
	entries := make([]entry, 0, len(modelSchemas))
	for _, model := range modelNames() {
		entries = append(entries, entry{Model: model, Href: s.schemaURI(model)})
	}
	s.writeJSON(w, r, http.StatusOK, "", map[string]any{
		"specification": s.basePath + "/openapi.json",
		"models":        entries,
	})
}

// handleSchema serves one model's schema as a standalone JSON Schema document: the
// model at the root, every schema it references bundled into $defs, and an $id that
// says where it came from. A consumer can therefore validate a payload with a plain
// JSON Schema validator, with no OpenAPI tooling and no second request.
func (s *Server) handleSchema(w http.ResponseWriter, r *http.Request) {
	model := r.PathValue("model")
	document, ok := s.schemaDocument(model)
	if !ok {
		s.writeProblem(w, r, codeNotFound, "no such model; the available models are listed at "+s.basePath+"/schemas")
		return
	}
	body, err := marshalJSON(document)
	if err != nil {
		s.writeProblem(w, r, codeInternal, "")
		return
	}
	s.writeStaticJSON(w, r, schemaMediaType, body)
}

// schemaDocument builds the standalone schema for a model.
func (s *Server) schemaDocument(model string) (map[string]any, bool) {
	name, ok := modelSchemas[model]
	if !ok {
		return nil, false
	}
	root, ok := rewriteRefs(s.spec.schemas[name]).(map[string]any)
	if !ok {
		return nil, false
	}
	document := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     s.schemaURI(model),
	}
	for key, value := range root {
		document[key] = value
	}
	// Every other component schema is bundled, so the document is self-contained: a
	// validator never has to fetch a second URL, and the model cannot be validated
	// against a mismatched version of a nested type.
	defs := map[string]any{}
	for other, schema := range s.spec.schemas {
		defs[other] = rewriteRefs(schema)
	}
	document["$defs"] = defs
	return document, true
}

// rewriteRefs deep-copies a schema, pointing every OpenAPI component reference at the
// bundled $defs instead. It is what turns a fragment of an OpenAPI document into a
// JSON Schema document that stands on its own.
//
// A discriminator's mapping is rewritten too: its values are references written as
// plain strings, so a union whose mapping still pointed into #/components would leave
// a generator following a URL the standalone document does not contain.
func rewriteRefs(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			switch {
			case key == "$ref":
				if ref, ok := nested.(string); ok {
					out[key] = bundledRef(ref)
					continue
				}
				out[key] = rewriteRefs(nested)
			case key == "discriminator" && isDiscriminator(nested):
				out[key] = rewriteDiscriminator(nested)
			default:
				out[key] = rewriteRefs(nested)
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, nested := range typed {
			out = append(out, rewriteRefs(nested))
		}
		return out
	default:
		return value
	}
}

// isDiscriminator reports whether a value under the key "discriminator" is one.
//
// A schema with a *property* called "discriminator" is an ordinary schema, and
// rewriting its properties as a mapping would corrupt it. The question is answered
// from the value itself, by the member OpenAPI requires a discriminator object to
// carry, rather than from the schema around it: a discriminator is legal on a base
// schema whose variants are composed with allOf, where there is no oneOf or anyOf
// anywhere near it, and such a mapping would then keep pointing at #/components in a
// document that has none.
func isDiscriminator(value any) bool {
	discriminator, ok := value.(map[string]any)
	if !ok {
		return false
	}
	propertyName, named := discriminator["propertyName"].(string)
	return named && propertyName != ""
}

// rewriteDiscriminator rewrites the reference strings in a discriminator's mapping.
func rewriteDiscriminator(value any) any {
	discriminator, ok := value.(map[string]any)
	if !ok {
		return rewriteRefs(value)
	}
	out := make(map[string]any, len(discriminator))
	for key, nested := range discriminator {
		mapping, isMapping := nested.(map[string]any)
		if key != "mapping" || !isMapping {
			out[key] = rewriteRefs(nested)
			continue
		}
		rewritten := make(map[string]any, len(mapping))
		for variant, target := range mapping {
			if ref, isRef := target.(string); isRef {
				rewritten[variant] = bundledRef(ref)
				continue
			}
			rewritten[variant] = rewriteRefs(target)
		}
		out[key] = rewritten
	}
	return out
}

// bundledRef points one component reference at the bundled $defs.
func bundledRef(ref string) string {
	return strings.Replace(ref, "#/components/schemas/", "#/$defs/", 1)
}

// handleProblemIndex lists every failure the API can report, so a consumer can build
// its error handling from the catalogue instead of from observed responses.
func (s *Server) handleProblemIndex(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		// Code identifies the problem kind.
		Code string `json:"code"`
		// Type is the URI the kind's document uses.
		Type string `json:"type"`
		// Title is the kind's short summary.
		Title string `json:"title"`
		// Status is the HTTP status it is reported with.
		Status int `json:"status"`
	}
	entries := make([]entry, 0, len(problemTypes))
	for _, code := range problemCodes() {
		kind := problemTypes[code]
		entries = append(entries, entry{
			Code:   string(code),
			Type:   s.problemTypeURI(code),
			Title:  kind.Title,
			Status: kind.Status,
		})
	}
	s.writeJSON(w, r, http.StatusOK, "", map[string]any{"problems": entries})
}

// handleProblemType serves one problem kind's description: what the type URI in a
// problem document resolves to, so the type is documentation rather than an opaque
// string.
func (s *Server) handleProblemType(w http.ResponseWriter, r *http.Request) {
	code := problemCode(r.PathValue("code"))
	kind, ok := problemTypes[code]
	if !ok {
		s.writeProblem(w, r, codeNotFound, "no such problem type; they are listed at "+s.basePath+"/problems")
		return
	}
	s.writeJSON(w, r, http.StatusOK, "", map[string]any{
		"code":        string(code),
		"type":        s.problemTypeURI(code),
		"title":       kind.Title,
		"status":      kind.Status,
		"description": kind.Description,
	})
}

// handleServiceDescription answers the API's entry point: what this API is, where its
// specification and schemas are, which resources it offers, and the vocabularies and
// bounds a consumer needs. One request from the base path is enough to discover
// everything else.
func (s *Server) handleServiceDescription(w http.ResponseWriter, r *http.Request) {
	type resourceEntry struct {
		// Name is the resource collection's name.
		Name string `json:"name"`
		// Href is where it is served.
		Href string `json:"href"`
		// Methods are the methods it offers.
		Methods []string `json:"methods"`
		// Schema is the model that describes its representation.
		Schema string `json:"schema,omitempty"`
	}
	info, _ := s.spec.document["info"].(map[string]any)
	title, _ := info["title"].(string)
	summary, _ := info["description"].(string)

	statuses := make([]string, 0, len(agenthub.WorkStatuses()))
	for _, status := range agenthub.WorkStatuses() {
		statuses = append(statuses, string(status))
	}
	kinds := make([]string, 0, len(agenthub.ItemKinds()))
	for _, kind := range agenthub.ItemKinds() {
		kinds = append(kinds, string(kind))
	}
	locationKinds := make([]string, 0, len(agenthub.LocationKinds()))
	for _, kind := range agenthub.LocationKinds() {
		locationKinds = append(locationKinds, string(kind))
	}

	s.writeJSON(w, r, http.StatusOK, modelServiceDescription, map[string]any{
		"name":        title,
		"description": summary,
		"version":     Version,
		"basePath":    s.basePath,
		"specification": map[string]any{
			"href": s.basePath + "/openapi.json",
			"type": openAPIMediaType,
		},
		"schemas":  s.basePath + "/schemas",
		"problems": s.basePath + "/problems",
		"health":   s.basePath + "/health",
		"resources": []resourceEntry{
			{Name: "active-work", Href: s.basePath + "/active-work", Methods: []string{"GET"}, Schema: s.schemaURI(modelActiveWorkCollection)},
			{Name: "fleets", Href: s.basePath + "/fleets", Methods: []string{"GET"}, Schema: s.schemaURI(modelFleetCollection)},
			{Name: "runs", Href: s.basePath + "/runs", Methods: []string{"GET"}, Schema: s.schemaURI(modelRunCollection)},
			{Name: "schedules", Href: s.basePath + "/schedules", Methods: []string{"GET"}, Schema: s.schemaURI(modelScheduleCollection)},
			{Name: "dismissals", Href: s.basePath + "/dismissals", Methods: []string{"GET", "POST"}, Schema: s.schemaURI(modelDismissalCollection)},
		},
		"vocabularies": map[string]any{
			"workStatus":   statuses,
			"itemKind":     kinds,
			"locationKind": locationKinds,
		},
		"limits": map[string]any{
			"defaultLimit":     agenthub.DefaultLimit,
			"maxLimit":         agenthub.MaxLimit,
			"maxRequestBytes":  s.maxBodyBytes,
			"requestTimeoutMs": s.timeout.Milliseconds(),
		},
	})
}

// handleAPICatalog answers the well-known catalogue: a linkset that points at this
// API's description and specification, so a consumer (or a portal that indexes
// services) can find the contract from the host alone, without being told the base
// path.
func (s *Server) handleAPICatalog(w http.ResponseWriter, r *http.Request) {
	body, err := marshalJSON(map[string]any{
		"linkset": []any{map[string]any{
			"anchor": s.basePath,
			"service-desc": []any{map[string]any{
				"href": s.basePath + "/openapi.json",
				"type": openAPIMediaType,
			}},
			"service-meta": []any{map[string]any{
				"href": s.basePath,
				"type": "application/json",
			}},
			"status": []any{map[string]any{
				"href": s.basePath + "/health",
				"type": "application/health+json",
			}},
		}},
	})
	if err != nil {
		s.writeProblem(w, r, codeInternal, "")
		return
	}
	s.writeStaticJSON(w, r, "application/linkset+json", body)
}

// writeStaticJSON serves a document that only changes when the server is rebuilt: the
// specification, a schema, the catalogue. It is cacheable for real (unlike a live
// read), and still carries an entity tag so a consumer can revalidate cheaply.
func (s *Server) writeStaticJSON(w http.ResponseWriter, r *http.Request, mediaType string, body []byte) {
	etag := entityTag(body)
	header := w.Header()
	header.Set("Content-Type", mediaType)
	header.Set("ETag", etag)
	header.Set("Cache-Control", "public, max-age=300")
	if matchesEntityTag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
