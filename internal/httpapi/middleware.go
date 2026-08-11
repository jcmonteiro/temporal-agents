package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"temporal-agents/internal/agenthub"
)

// The middlewares here are the API's security and operability posture, applied to
// every response whether or not the deployment is exposed. The API is designed as if
// it were on the open internet — it binds to loopback by default, but "it is only
// local" is an assumption about a deployment, not a property of the code, and the
// data behind it (goals, prompts, failure text) is exactly what must not leak.

// requestIDHeader carries a request's identifier, both back to the consumer and into
// the log, so a problem a consumer reports can be found in the server's log without
// the response having to disclose anything.
const requestIDHeader = "X-Request-Id"

// requestIDKey is the context key the request identifier is carried under.
type requestIDKey struct{}

// requestIDFrom returns the request's identifier, or "" outside a request.
func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// withRequestID assigns every request an identifier and echoes it back.
//
// A client-supplied identifier is deliberately not trusted: it would put an
// unvalidated value into the log and into a response header. The identifier is
// generated here.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// securityHeaders sets the headers that constrain what a response may be used for.
// They cost nothing and close off whole classes of misuse: a JSON document must not
// be sniffed into something executable, framed, used as a script source, or leak the
// URL it was fetched from through a referrer.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Cross-Origin-Resource-Policy", "same-origin")
		header.Set("X-Frame-Options", "DENY")
		// A JSON API needs no resources of its own, so the strictest policy applies.
		// The static-asset handler replaces it with a policy an application can run
		// under (see staticHandler).
		header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

// recoverPanics turns a defect into a problem document instead of a dropped
// connection, and writes the cause to the log where the consumer cannot see it.
func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if cause := recover(); cause != nil {
				s.logger.Error("the request handler panicked",
					"requestId", requestIDFrom(r.Context()),
					"method", r.Method,
					"path", r.URL.EscapedPath(),
					"cause", fmt.Sprint(cause))
				s.writeProblem(w, r, codeInternal, "")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// withTimeout bounds how long one request may take, so a dependency that stops
// answering cannot hold connections open indefinitely. The budget is the request's
// context deadline, so a handler's reads are cancelled with it rather than left
// running after the answer is gone.
func (s *Server) withTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Event streams are bounded by authentication, concurrency, and the caller's
		// connection lifetime. Applying the ordinary request budget would cut every
		// healthy stream off after 30 seconds.
		if strings.HasSuffix(r.URL.Path, "/events") {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// rateLimit bounds the request rate the server accepts, so a consumer polling in a
// tight loop degrades itself rather than the server. It is one bucket for the whole
// process: this is a single-operator API, so per-client accounting would add state
// without protecting anything more.
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.limiter != nil && !s.limiter.Allow() {
			s.writeProblem(w, r, codeTooManyRequests, "the request rate exceeds what this server accepts")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// canonicalHost removes the optional port and normalizes case and a DNS root dot.
// It returns an empty string for malformed input, which can never match the allowlist.
func canonicalHost(value string) string {
	host := strings.TrimSpace(value)
	if split, _, err := net.SplitHostPort(host); err == nil {
		host = split
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	} else if strings.Count(host, ":") > 1 {
		// A bare IPv6 address is valid as a configured allowlist entry. A request Host
		// with a port must use brackets and is rejected by the branch above.
		if net.ParseIP(host) == nil {
			return ""
		}
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return host
}

// requireHost rejects every HTTP Host not named by the server configuration. This
// check is the loopback service's DNS-rebinding boundary; CORS cannot provide it.
func (s *Server) requireHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, allowed := s.allowedHosts[canonicalHost(r.Host)]; !allowed {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// cors answers cross-origin requests, and only for origins an operator listed.
// There is no default allowance and no wildcard. An unlisted supplied Origin is
// rejected, because omitting a response header does not stop a same-origin request
// after DNS rebinding.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !s.allowedOrigins[origin] {
				http.Error(w, "invalid origin", http.StatusForbidden)
				return
			}
			header := w.Header()
			header.Set("Access-Control-Allow-Origin", origin)
			header.Set("Access-Control-Allow-Credentials", "true")
			// The answer depends on the request's origin, so a cache must not serve one
			// origin's response to another.
			header.Add("Vary", "Origin")
			header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-None-Match")
			header.Set("Access-Control-Expose-Headers", strings.Join([]string{
				"ETag", "Link", requestIDHeader, "Retry-After", "Deprecation", "Sunset",
			}, ", "))
			header.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			// A preflight is answered by the middleware, not by a resource: the resources
			// themselves offer no OPTIONS.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// deprecation announces the API's lifecycle when an operator has set it, using the
// standard headers so a consumer's tooling can see a sunset coming without reading
// release notes. Nothing is announced by default: an API that is not deprecated must
// not claim to be.
func (s *Server) deprecation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.deprecatedSince.IsZero() {
			w.Header().Set("Deprecation", "@"+strconv.FormatInt(s.deprecatedSince.UTC().Unix(), 10))
			w.Header().Add("Link", `<`+s.basePath+`>; rel="deprecation"; type="application/json"`)
		}
		if !s.sunsetAt.IsZero() {
			w.Header().Set("Sunset", s.sunsetAt.UTC().Format(http.TimeFormat))
		}
		next.ServeHTTP(w, r)
	})
}

// accessLog records one line per request: what was asked, how it was answered and how
// long it took, with the request's identifier so a consumer's problem document can be
// found here. It logs the path only — the API takes no secrets in a query string, and
// a log that copies request bodies is a leak waiting to happen.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := s.now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.Info("served an API request",
			"requestId", requestIDFrom(r.Context()),
			"method", r.Method,
			"path", r.URL.EscapedPath(),
			"status", recorder.status,
			"bytes", recorder.written,
			"durationMs", s.now().Sub(started).Milliseconds())
	})
}

// statusRecorder remembers what a handler answered so the access log can report it.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	written     int
	wroteHeader bool
}

// Unwrap lets ResponseController reach optional interfaces such as Flusher. Without
// it, access logging would turn every SSE response into an unflushable buffer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) WriteHeader(status int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	n, err := s.ResponseWriter.Write(b)
	s.written += n
	return n, err
}

// writeJSON answers a GET with a JSON document, an entity tag derived from the body,
// and the link that says which schema describes it.
//
// The entity tag is what makes this read cheap to poll and cheap to put behind a
// cache or CDN: a consumer that already has the current answer gets 304 and no body.
// It is derived from the bytes, so it is correct by construction rather than by
// remembering to bump a version.
func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, model string, payload any) {
	body, err := marshalJSON(payload)
	if err != nil {
		s.logger.Error("could not encode a response",
			"requestId", requestIDFrom(r.Context()), "path", r.URL.EscapedPath(), "error", err.Error())
		s.writeProblem(w, r, codeInternal, "")
		return
	}
	header := w.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	if model != "" {
		// Which schema describes this document, as a link rather than as a field, so the
		// payload stays exactly the model and a consumer can still validate it.
		header.Add("Link", fmt.Sprintf("<%s>; rel=\"describedby\"; type=\"application/schema+json\"", s.schemaURI(model)))
	}
	etag := entityTag(body)
	header.Set("ETag", etag)
	// The answer is derived from live state, so it may be stored but must be
	// revalidated; the entity tag makes revalidation a single cheap round trip.
	header.Set("Cache-Control", "no-cache")

	if r.Method == http.MethodGet && matchesEntityTag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	header.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}

// entityTag is a strong entity tag over a response body: the same bytes always
// produce the same tag, and different bytes practically never collide.
func entityTag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`
}

// matchesEntityTag reports whether a client's If-None-Match header covers tag,
// honouring the "*" form and a comma-separated list.
func matchesEntityTag(header, tag string) bool {
	if header == "" {
		return false
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == tag {
			return true
		}
		// A weak tag compares equal to its strong form for the purposes of
		// revalidation.
		if strings.TrimPrefix(candidate, "W/") == tag {
			return true
		}
	}
	return false
}

// decodeJSONBody reads a write request's body into target, refusing anything that is
// not what this API accepts: a wrong media type, an oversized body, a malformed
// document, an unknown field, or more than one document.
//
// Strictness is deliberate. An unknown field is refused rather than ignored, because
// silently dropping a field a consumer believed in is how a client and a server come
// to disagree about what was asked.
func (s *Server) decodeJSONBody(w http.ResponseWriter, r *http.Request, target any) bool {
	if mediaType := r.Header.Get("Content-Type"); !isJSONMediaType(mediaType) {
		s.writeProblem(w, r, codeUnsupportedMediaType,
			"send the body as application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	decoder := newStrictJSONDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.writeProblem(w, r, codeRequestTooLarge,
				fmt.Sprintf("the body must be at most %d bytes", s.maxBodyBytes))
			return false
		}
		s.writeProblem(w, r, codeInvalidRequest, "the body is not a valid document: "+err.Error())
		return false
	}
	// A second decode must reach the end of the body. Decoder.More only applies
	// inside arrays and objects, so it cannot detect two top-level documents.
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		s.writeProblem(w, r, codeInvalidRequest, "the body must carry exactly one JSON document")
		return false
	}
	return true
}

// isJSONMediaType reports whether a Content-Type header names JSON. Parameters are
// accepted when they are syntactically valid (for example charset=utf-8); malformed
// parameters are refused rather than ignored.
func isJSONMediaType(header string) bool {
	mediaType, _, err := mime.ParseMediaType(header)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

// limitParam reads the "limit" query parameter through the core's own validation, so
// the transport cannot accidentally offer a different paging contract than the one
// the model publishes.
func (s *Server) limitParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	query, ok := s.parseQuery(w, r)
	if !ok {
		return 0, false
	}
	if len(query["limit"]) > 1 {
		s.writeProblem(w, r, codeInvalidRequest, "query parameter is repeated")
		return 0, false
	}
	return s.parseLimit(w, r, query.Get("limit"))
}

func (s *Server) parseLimit(w http.ResponseWriter, r *http.Request, parameter string) (int, bool) {
	raw := strings.TrimSpace(parameter)
	if raw == "" {
		return validatedLimit(0)
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		s.writeProblem(w, r, codeInvalidRequest, "limit must be a whole number")
		return 0, false
	}
	limit, verr := validatedLimitOrError(value)
	if verr != nil {
		s.writeProblem(w, r, codeInvalidRequest, verr.Error())
		return 0, false
	}
	return limit, true
}

func (s *Server) parseQuery(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		s.writeProblem(w, r, codeInvalidRequest, "query is invalid")
		return nil, false
	}
	return query, true
}

const maxCursorBytes = 4 << 10

func (s *Server) activeWorkQuery(w http.ResponseWriter, r *http.Request) (agenthub.PageQuery, bool) {
	query, ok := s.parseQuery(w, r)
	if !ok {
		return agenthub.PageQuery{}, false
	}
	if len(query["limit"]) > 1 || len(query["cursor"]) > 1 {
		s.writeProblem(w, r, codeInvalidRequest, "query parameter is repeated")
		return agenthub.PageQuery{}, false
	}
	limit, ok := s.parseLimit(w, r, query.Get("limit"))
	if !ok {
		return agenthub.PageQuery{}, false
	}
	raw := strings.TrimSpace(query.Get("cursor"))
	if raw == "" {
		if query.Has("cursor") {
			s.writeProblem(w, r, codeInvalidRequest, "cursor is invalid")
			return agenthub.PageQuery{}, false
		}
		return agenthub.PageQuery{Limit: limit}, true
	}
	cursor, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(cursor) == 0 || len(cursor) > maxCursorBytes {
		s.writeProblem(w, r, codeInvalidRequest, "cursor is invalid")
		return agenthub.PageQuery{}, false
	}
	return agenthub.PageQuery{Limit: limit, Cursor: cursor}, true
}

func (s *Server) nextPage(r *http.Request, cursor []byte) string {
	if len(cursor) == 0 {
		return ""
	}
	query := r.URL.Query()
	query.Set("cursor", base64.RawURLEncoding.EncodeToString(cursor))
	return r.URL.Path + "?" + query.Encode()
}

// newLimiter builds the process-wide rate limiter, or nil when rate limiting is
// switched off.
func newLimiter(perSecond float64, burst int) *rate.Limiter {
	if perSecond <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = 1
	}
	return rate.NewLimiter(rate.Limit(perSecond), burst)
}

// retryAfterDefault is how long a consumer is asked to wait after a transient
// failure. It is short: the dependencies this API reads are local.
const retryAfterDefault = 5 * time.Second
