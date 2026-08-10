package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

// Every failure this API reports is a problem document: one machine-readable shape
// for all of them, with a type that a consumer can branch on and that resolves to a
// description of what the type means (see the problems endpoint). A consumer never
// has to parse a message to find out what went wrong, and never receives an
// unstructured error page.
//
// Nothing internal leaks into a problem: the detail is written for the consumer, and
// the underlying cause (a DSN, a driver error, a stack trace) goes to the server's
// log instead. An error message is part of the contract too, and an error message
// that discloses the machinery is a security defect even on a loopback interface.

// problemMediaType is the media type of a problem document (RFC 9457).
const problemMediaType = "application/problem+json"

// problemCode identifies a kind of failure. It is the last segment of the problem's
// type URI, so a consumer branches on a stable identifier rather than on a status
// code shared by unrelated failures.
type problemCode string

const (
	// codeInvalidRequest is a request this API cannot act on: a malformed body, an
	// out-of-range parameter, an identifier of the wrong shape.
	codeInvalidRequest problemCode = "invalid-request"
	// codeNotFound is a resource that does not exist.
	codeNotFound problemCode = "not-found"
	// codeMethodNotAllowed is a method this resource does not offer.
	codeMethodNotAllowed problemCode = "method-not-allowed"
	// codeUnsupportedMediaType is a request body in a format this API does not read.
	codeUnsupportedMediaType problemCode = "unsupported-media-type"
	// codeRequestTooLarge is a request body above the accepted size.
	codeRequestTooLarge problemCode = "request-too-large"
	// codeNotDismissible is a dismissal asked for work that has not finished.
	codeNotDismissible problemCode = "not-dismissible"
	// codeNotAPlace is a registration of a directory the hub cannot work in: nothing
	// is there, or no repository holds it.
	codeNotAPlace problemCode = "not-a-place"
	// codePlaceIsBusy is a start refused because work is already running in the
	// place it names.
	codePlaceIsBusy problemCode = "place-is-busy"
	// codeAuthenticationRequired is a request that carries no credential this hub
	// accepts: no session, and no configured token.
	codeAuthenticationRequired problemCode = "authentication-required"
	// codeSignInFailed is a callback from the identity provider that cannot be
	// completed: it is bound to no sign-in this server started, it has been used
	// already, it expired, or the provider refused it.
	codeSignInFailed problemCode = "sign-in-failed"
	// codeCrossSiteRequest is a change requested from another site.
	codeCrossSiteRequest problemCode = "cross-site-request"
	// codeTooManyRequests is a caller going faster than the server accepts.
	codeTooManyRequests problemCode = "too-many-requests"
	// codeDependencyUnavailable is a dependency of the read path being unreachable:
	// the request was fine and may be retried.
	codeDependencyUnavailable problemCode = "dependency-unavailable"
	// codeTimeout is a request that took longer than the server's budget.
	codeTimeout problemCode = "timeout"
	// codeInternal is a defect in this server. It carries no detail beyond that, on
	// purpose.
	codeInternal problemCode = "internal-error"
)

// problemType describes one kind of failure: the title a consumer may show, the
// status it is reported with, and an explanation served at the type's own URI so the
// type is not an opaque string.
type problemType struct {
	// Title is the short, human-readable summary of the problem kind. It never
	// changes for a given code, so a consumer may match on either.
	Title string
	// Status is the HTTP status this kind is reported with.
	Status int
	// Description explains when the problem occurs and what a consumer can do about
	// it. It is what the type URI resolves to.
	Description string
}

// problemTypes is the catalogue of every failure this API can report. It is a
// closed set: a handler picks a code from it, so a new kind of failure has to be
// named, documented and given a status here before it can reach a consumer.
var problemTypes = map[problemCode]problemType{
	codeInvalidRequest: {
		Title:  "The request is invalid",
		Status: http.StatusBadRequest,
		Description: "The request could not be acted on as sent: a parameter is out of range, " +
			"a body is malformed or carries an unknown field, or an identifier has the wrong shape. " +
			"The detail names the offending part. Retrying the same request will fail again.",
	},
	codeNotFound: {
		Title:  "No such resource",
		Status: http.StatusNotFound,
		Description: "The addressed resource does not exist. For a fleet or a run this also means " +
			"the orchestrator no longer knows the execution and nothing was recorded for it.",
	},
	codeMethodNotAllowed: {
		Title:  "The method is not allowed on this resource",
		Status: http.StatusMethodNotAllowed,
		Description: "The resource exists but does not offer this method. The Allow header lists " +
			"the methods it does offer. Almost everything here is read-only: the sole write is the " +
			"dismissal of a finished item.",
	},
	codeUnsupportedMediaType: {
		Title:  "The request body's media type is not supported",
		Status: http.StatusUnsupportedMediaType,
		Description: "A write must send application/json. The media type is checked rather than " +
			"guessed, so a body that is not what it claims is refused instead of misread.",
	},
	codeRequestTooLarge: {
		Title:  "The request body is too large",
		Status: http.StatusRequestEntityTooLarge,
		Description: "A write body is bounded, so a request cannot make the server allocate an " +
			"arbitrary amount of memory. The bodies this API accepts are a few fields long.",
	},
	codeNotDismissible: {
		Title:  "The item cannot be dismissed",
		Status: http.StatusConflict,
		Description: "Dismissing is view state over finished work: only an item that has finished " +
			"(done or failed) can be hidden, and a schedule never can because it has no finished " +
			"state. Wait for the work to settle, or leave it visible.",
	},
	codeNotAPlace: {
		Title:  "The hub cannot work in that directory",
		Status: http.StatusUnprocessableEntity,
		Description: "A place must be a directory that exists on the machine the work runs on and " +
			"that a repository holds: the hub works by branching, committing and reviewing. The " +
			"detail says which of the two is missing. Nothing was registered.",
	},
	codePlaceIsBusy: {
		Title:  "Work is already running in that place",
		Status: http.StatusConflict,
		Description: "Two loops in one working tree stash and commit over each other, so a second " +
			"one is refused rather than allowed to corrupt it. The detail names the work that is " +
			"already running there. Wait for it to settle, or start the work in another place.",
	},
	codeAuthenticationRequired: {
		Title:  "Authentication is required",
		Status: http.StatusUnauthorized,
		Description: "The request carries no credential this deployment accepts. A person signs " +
			"in with the identity provider (follow the Link header with rel=\"authenticate\"); a " +
			"script sends the configured token in the Authorization header. A missing, expired, " +
			"ended and incorrect credential all receive this same response.",
	},
	codeSignInFailed: {
		Title:  "The sign-in could not be completed",
		Status: http.StatusBadRequest,
		Description: "The callback from the identity provider does not belong to a sign-in this " +
			"server started for this browser, has been used already, took too long, or was refused " +
			"by the provider. Which of those it was is deliberately not disclosed. Start again at " +
			"the sign-in endpoint.",
	},
	codeCrossSiteRequest: {
		Title:  "The request came from another site",
		Status: http.StatusForbidden,
		Description: "A change must be requested by this application, from this site. Binding to " +
			"loopback is no protection here: any page a browser visits can send a request to a " +
			"local port, and this hub can start agent work. A script or the CLI, which carry no " +
			"ambient credential for another site to borrow, are unaffected.",
	},
	codeTooManyRequests: {
		Title:  "Too many requests",
		Status: http.StatusTooManyRequests,
		Description: "The server accepts a bounded rate of requests so that a polling consumer " +
			"cannot exhaust it. Honour the Retry-After header and poll less often.",
	},
	codeDependencyUnavailable: {
		Title:  "A dependency is unavailable",
		Status: http.StatusServiceUnavailable,
		Description: "The request was valid but a source the answer needs — the orchestrator, the " +
			"execution record, the dismissal store — could not be reached. The API reports this " +
			"rather than answering with a partial overview, because a half-empty overview reads as " +
			"work having disappeared. Retry after the interval in Retry-After.",
	},
	codeTimeout: {
		Title:  "The request took too long",
		Status: http.StatusGatewayTimeout,
		Description: "The server bounds how long one request may take, so a slow dependency cannot " +
			"hold a connection open indefinitely. Retry, optionally with a smaller limit.",
	},
	codeInternal: {
		Title:  "The server failed to handle the request",
		Status: http.StatusInternalServerError,
		Description: "A defect in this server. The cause is written to the server's log with the " +
			"request's identifier and is deliberately not disclosed in the response.",
	},
}

// problemCodes lists every code in a stable order, for the catalogue endpoint and
// for the test that pins the specification against this registry.
func problemCodes() []problemCode {
	codes := make([]problemCode, 0, len(problemTypes))
	for code := range problemTypes {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	return codes
}

// problemDocument is the body of every failure response (RFC 9457).
type problemDocument struct {
	// Type identifies the problem kind, as a URI that resolves to the kind's
	// description.
	Type string `json:"type"`
	// Title is the problem kind's short summary.
	Title string `json:"title"`
	// Status is the HTTP status, repeated in the body so a stored or forwarded
	// document is still self-describing.
	Status int `json:"status"`
	// Detail explains this occurrence, in terms of the request rather than of the
	// server's internals. It is empty when there is nothing to add to the title.
	Detail string `json:"detail,omitempty"`
	// Instance is the request's path, so a document found in a log can be traced
	// back to what was asked.
	Instance string `json:"instance,omitempty"`
	// RequestID correlates the document with the server's log entry for the same
	// request, which is how a consumer reports a problem without the server having
	// to disclose its internals in the response.
	RequestID string `json:"requestId,omitempty"`
	// ConflictingRunID names the work a refusal collided with, where a refusal
	// collided with something. It is an extension member (RFC 9457 allows them), and
	// it exists so a consumer can link to what is in the way instead of reading the
	// identity out of a sentence written for a person.
	ConflictingRunID string `json:"conflictingRunId,omitempty"`
}

// problemTypeURI is the URI a problem code resolves to: the catalogue entry for that
// kind, under the API's own base path.
func (s *Server) problemTypeURI(code problemCode) string {
	return s.basePath + "/problems/" + string(code)
}

// writeProblem answers a request with a problem document. detail is the
// consumer-facing explanation; anything the consumer must not see belongs in the log
// instead (see logProblem).
func (s *Server) writeProblem(w http.ResponseWriter, r *http.Request, code problemCode, detail string) {
	s.writeProblemAbout(w, r, code, detail, "")
}

// writeProblemAbout answers with a problem document that also names the resource the
// refusal is about, for the refusals that collide with one.
func (s *Server) writeProblemAbout(w http.ResponseWriter, r *http.Request, code problemCode, detail, conflictingRunID string) {
	kind, ok := problemTypes[code]
	if !ok {
		// A code that is not in the catalogue is itself a defect; report the generic
		// one rather than a document with an unresolvable type.
		code, kind = codeInternal, problemTypes[codeInternal]
	}
	document := problemDocument{
		Type:      s.problemTypeURI(code),
		Title:     kind.Title,
		Status:    kind.Status,
		Detail:    detail,
		Instance:  r.URL.EscapedPath(),
		RequestID: requestIDFrom(r.Context()),

		ConflictingRunID: conflictingRunID,
	}
	body, err := json.Marshal(document)
	if err != nil {
		// Marshalling a fixed struct of strings cannot fail; if it somehow does, say so
		// in the status and nothing else.
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if code == codeDependencyUnavailable || code == codeTooManyRequests {
		// Both are transient by definition, so the answer says when to come back
		// instead of leaving a consumer to guess (and hammer).
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(s.retryAfter.Seconds())))
	}
	w.Header().Set("Content-Type", problemMediaType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	// A failure is never cacheable: the next identical request may well succeed.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(kind.Status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}
