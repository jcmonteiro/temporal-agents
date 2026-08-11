package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/identity"
	"temporal-agents/internal/steering"
	"temporal-agents/internal/steering/steeringtest"
)

type streamWriter struct {
	header  http.Header
	mu      sync.Mutex
	body    bytes.Buffer
	flushed chan string
}

func newStreamWriter() *streamWriter {
	return &streamWriter{header: http.Header{}, flushed: make(chan string, 20)}
}

func (w *streamWriter) Header() http.Header { return w.header }
func (w *streamWriter) WriteHeader(int)     {}
func (w *streamWriter) Write(body []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(body)
}
func (w *streamWriter) Flush() {
	w.mu.Lock()
	body := w.body.String()
	w.mu.Unlock()
	w.flushed <- body
}

func streamServer(t *testing.T, store *steeringtest.Store, mutate ...func(*Options)) *Server {
	t.Helper()
	service, err := steering.NewService(store, store)
	require.NoError(t, err)
	changes := []func(*Options){func(options *Options) {
		options.Steering = service
		options.StreamPollInterval = 5 * time.Millisecond
	}}
	changes = append(changes, mutate...)
	return newTestServer(t, &viewStub{}, changes...)
}

func waitForStream(t *testing.T, writer *streamWriter, contains string) string {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case body := <-writer.flushed:
			if strings.Contains(body, contains) {
				return body
			}
		case <-deadline:
			t.Fatalf("stream did not contain %q", contains)
		}
	}
}

func TestAConversationStreamResumesWithoutGapsOrDuplicates(t *testing.T) {
	store := steeringtest.New().WithSession(aWaitingSession(fixedNow))
	for _, text := range []string{"one", "two", "three"} {
		_, err := store.AppendMessage(context.Background(), steering.Message{
			SessionID: "steering-review-1", Role: steering.RoleAgent, Text: text, At: fixedNow,
		})
		require.NoError(t, err)
	}
	server := streamServer(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	request := newRequest(http.MethodGet,
		BasePath+"/steering/sessions/steering-review-1/events?after=1", nil).WithContext(ctx)
	writer := newStreamWriter()

	go server.ServeHTTP(writer, request)
	body := waitForStream(t, writer, "id: 3")
	cancel()

	require.NotContains(t, body, "id: 1\n")
	require.Equal(t, 1, strings.Count(body, "id: 2\n"))
	require.Equal(t, 1, strings.Count(body, "id: 3\n"))
	require.Less(t, strings.Index(body, "id: 2\n"), strings.Index(body, "id: 3\n"))
}

func TestTwoConversationReadersSeeTheSameSequence(t *testing.T) {
	store := steeringtest.New().WithSession(aWaitingSession(fixedNow))
	_, err := store.AppendMessage(context.Background(), steering.Message{
		SessionID: "steering-review-1", Role: steering.RoleAgent, Text: "same", At: fixedNow,
	})
	require.NoError(t, err)
	server := streamServer(t, store)

	bodies := make([]string, 2)
	cancels := make([]context.CancelFunc, 2)
	for i := range bodies {
		ctx, cancel := context.WithCancel(context.Background())
		cancels[i] = cancel
		writer := newStreamWriter()
		request := newRequest(http.MethodGet,
			BasePath+"/steering/sessions/steering-review-1/events", nil).WithContext(ctx)
		go server.ServeHTTP(writer, request)
		bodies[i] = waitForStream(t, writer, "id: 1")
	}
	for _, cancel := range cancels {
		cancel()
	}
	require.Contains(t, bodies[0], `"text":"same"`)
	require.Contains(t, bodies[1], `"text":"same"`)
}

func TestAnExpiredCredentialEndsAStreamWithAClearEvent(t *testing.T) {
	store := steeringtest.New()
	checks := 0
	authenticator := authenticatorFunc(func(context.Context, identity.Credential) (identity.Principal, error) {
		checks++
		if checks == 1 {
			return identity.Principal{Issuer: "test", Subject: "ada"}, nil
		}
		return identity.Principal{}, identity.ErrUnauthenticated
	})
	server := streamServer(t, store, func(options *Options) {
		options.AllowUnauthenticated = false
		options.Authenticator = authenticator
	})
	request := newRequest(http.MethodGet, BasePath+"/events", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "expiring"})
	writer := newStreamWriter()

	done := make(chan struct{})
	go func() {
		server.ServeHTTP(writer, request)
		close(done)
	}()
	body := waitForStream(t, writer, "auth-expired")

	require.Contains(t, body, `"action":"sign-in"`)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the expired stream did not end")
	}
}

var _ http.Flusher = (*streamWriter)(nil)
