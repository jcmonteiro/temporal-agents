package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const streamBatchSize = 100

type streamLimiter struct {
	mu             sync.Mutex
	byPrincipal    map[string]int
	bySession      map[string]int
	principalLimit int
	sessionLimit   int
}

func newStreamLimiter(principalLimit, sessionLimit int) *streamLimiter {
	return &streamLimiter{
		byPrincipal: map[string]int{}, bySession: map[string]int{},
		principalLimit: principalLimit, sessionLimit: sessionLimit,
	}
}

func (l *streamLimiter) acquire(principal, session string) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byPrincipal[principal] >= l.principalLimit ||
		(session != "" && l.bySession[session] >= l.sessionLimit) {
		return nil, false
	}
	l.byPrincipal[principal]++
	if session != "" {
		l.bySession[session]++
	}
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.byPrincipal[principal]--
		if session != "" {
			l.bySession[session]--
		}
	}, true
}

type hubEventResource struct {
	Type      string  `json:"type"`
	SessionID string  `json:"sessionId,omitempty"`
	ItemID    string  `json:"itemId,omitempty"`
	At        *string `json:"at"`
}

func streamPosition(r *http.Request) (int64, error) {
	value := strings.TrimSpace(r.URL.Query().Get("after"))
	if value == "" {
		value = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if value == "" {
		return 0, nil
	}
	position, err := strconv.ParseInt(value, 10, 64)
	if err != nil || position < 0 {
		return 0, errors.New("the stream position must be a non-negative integer")
	}
	return position, nil
}

func (s *Server) beginStream(w http.ResponseWriter, r *http.Request, session string) (func(), bool) {
	principal := "anonymous"
	if authenticated, ok := PrincipalFrom(r.Context()); ok {
		principal = authenticated.ID()
	}
	release, ok := s.streams.acquire(principal, session)
	if !ok {
		s.writeProblem(w, r, codeTooManyRequests, "too many event streams are already open for this credential")
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	return release, true
}

func writeSSE(w http.ResponseWriter, sequence int64, event string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if sequence > 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", sequence); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

func (s *Server) credentialValid(r *http.Request) bool {
	if s.authenticator == nil {
		return true
	}
	_, err := s.authenticator.Authenticate(r.Context(), credentialFrom(r))
	return err == nil
}

func (s *Server) handleConversationEvents(w http.ResponseWriter, r *http.Request) {
	position, err := streamPosition(r)
	if err != nil {
		s.writeProblem(w, r, codeInvalidRequest, err.Error())
		return
	}
	sessionID := r.PathValue("id")
	release, ok := s.beginStream(w, r, sessionID)
	if !ok {
		return
	}
	defer release()

	ticker := time.NewTicker(s.streamPoll)
	defer ticker.Stop()
	for {
		messages, err := s.steering.ConversationMessages(r.Context(), sessionID, position)
		if err != nil {
			_ = writeSSE(w, 0, "stream-error", map[string]string{"detail": "the conversation is unavailable"})
			return
		}
		for _, message := range messages {
			resource := steeringMessageResource{
				Sequence: message.Sequence, Role: string(message.Role), Author: message.Author,
				Text: message.Text, Tokens: message.Tokens, At: timestamp(message.At),
			}
			if err := writeSSE(w, message.Sequence, "message", resource); err != nil {
				return
			}
			position = message.Sequence
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !s.credentialValid(r) {
				_ = writeSSE(w, 0, "auth-expired", map[string]string{"action": "sign-in"})
				return
			}
		}
	}
}

func (s *Server) handleHubEvents(w http.ResponseWriter, r *http.Request) {
	position, err := streamPosition(r)
	if err != nil {
		s.writeProblem(w, r, codeInvalidRequest, err.Error())
		return
	}
	release, ok := s.beginStream(w, r, "")
	if !ok {
		return
	}
	defer release()

	ticker := time.NewTicker(s.streamPoll)
	defer ticker.Stop()
	for {
		events, err := s.steering.Events(r.Context(), position, streamBatchSize)
		if err != nil {
			_ = writeSSE(w, 0, "stream-error", map[string]string{"detail": "hub events are unavailable"})
			return
		}
		for _, event := range events {
			resource := hubEventResource{
				Type: event.Type, SessionID: event.SessionID, ItemID: event.ItemID, At: timestamp(event.At),
			}
			if err := writeSSE(w, event.Sequence, "hub", resource); err != nil {
				return
			}
			position = event.Sequence
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !s.credentialValid(r) {
				_ = writeSSE(w, 0, "auth-expired", map[string]string{"action": "sign-in"})
				return
			}
		}
	}
}
