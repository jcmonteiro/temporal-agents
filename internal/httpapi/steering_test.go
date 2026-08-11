package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/place"
	"temporal-agents/internal/steering"
	"temporal-agents/internal/steering/steeringtest"
)

// These tests drive the steering HTTP surface through the real application service.
// The store and signal edge use the context's in-memory adapter, so assertions are
// about resources, durable decisions, and delivered outcomes rather than handler calls.

const steeringTestToken = "01234567890123456789012345678901"

func aWaitingSession(opened time.Time) steering.Session {
	return steering.Session{
		ID:       "steering-review-1",
		ItemID:   "review-1",
		Round:    steering.RoundLocalReview,
		Material: "The retry hides the original error.",
		Place:    place.Facts{Directory: "/src/pricing"},
		OpenedAt: opened,
		State:    steering.StateWaiting,
	}
}

type apiQuestioner struct {
	store *steeringtest.Store
}

func (q apiQuestioner) RunQuestionTurn(ctx context.Context, turn steering.QuestionTurn) error {
	text := "Which callers depend on the wrapped cause?"
	if turn.Finish {
		text = "Keep the retry and preserve the wrapped cause."
	}
	_, err := q.store.AppendMessage(ctx, steering.Message{
		SessionID: turn.SessionID, Role: steering.RoleAgent, Text: text, Tokens: 40, At: fixedNow,
	})
	if err != nil {
		return err
	}
	if turn.Finish {
		return q.store.SetGuidance(ctx, turn.SessionID, text)
	}
	return nil
}

func defaultSteeringView(t *testing.T) SteeringView {
	t.Helper()
	store := steeringtest.New()
	service, err := steering.NewService(store, store)
	require.NoError(t, err)
	return service
}

func newSteeringServer(t *testing.T, store *steeringtest.Store) *Server {
	t.Helper()
	service, err := steering.NewService(store, store)
	require.NoError(t, err)
	service.Now = func() time.Time { return fixedNow }
	service.Questioner = apiQuestioner{store: store}
	return newTestServer(t, &viewStub{}, func(options *Options) {
		options.AllowUnauthenticated = false
		options.AuthToken = steeringTestToken
		options.Steering = service
	})
}

func steeringRequest(
	t *testing.T,
	server *Server,
	method string,
	target string,
	body string,
	prepare ...func(*http.Request),
) *httptest.ResponseRecorder {
	t.Helper()
	request := newRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+steeringTestToken)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for _, change := range prepare {
		change(request)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func TestAWaitingSteeringSessionCanBeFoundAndRead(t *testing.T) {
	opened := fixedNow.Add(-2 * time.Hour)
	store := steeringtest.New().WithSession(aWaitingSession(opened))
	server := newSteeringServer(t, store)

	listed := steeringRequest(t, server, http.MethodGet, BasePath+"/steering/sessions", "")
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var collection struct {
		Items []steeringSessionResource `json:"items"`
		Count int                       `json:"count"`
	}
	decodeResponse(t, listed, &collection)
	require.Equal(t, 1, collection.Count)
	require.Equal(t, "steering-review-1", collection.Items[0].ID)
	require.Empty(t, collection.Items[0].Material, "the collection must not copy review material")

	read := steeringRequest(t, server, http.MethodGet,
		BasePath+"/steering/sessions/steering-review-1", "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	var session steeringSessionResource
	decodeResponse(t, read, &session)
	require.Equal(t, "The retry hides the original error.", session.Material)
	require.Equal(t, "directory", string(session.Locations[0].Kind))
	require.Equal(t, opened.Format(time.RFC3339Nano), *session.WaitingSince)
}

func TestAnAuthenticatedOperatorDecidesAWaitingSessionExactlyOnce(t *testing.T) {
	store := steeringtest.New().WithSession(aWaitingSession(fixedNow.Add(-time.Hour)))
	server := newSteeringServer(t, store)
	body, err := json.Marshal(steeringDecisionRequest{Decision: "guide", Guidance: "keep the retry"})
	require.NoError(t, err)

	decided := steeringRequest(t, server, http.MethodPost,
		BasePath+"/steering/sessions/steering-review-1/decision", string(body))
	require.Equal(t, http.StatusOK, decided.Code, decided.Body.String())

	repeat := steeringRequest(t, server, http.MethodPost,
		BasePath+"/steering/sessions/steering-review-1/decision", `{"decision":"stop"}`)
	require.Equal(t, http.StatusOK, repeat.Code, repeat.Body.String())
	var resource steeringSessionResource
	decodeResponse(t, repeat, &resource)
	require.Equal(t, "guide", resource.Decision)
	require.Equal(t, "keep the retry", resource.Guidance)
	require.Len(t, store.Deliveries(), 2)
	require.Equal(t, steering.ChoiceGuide, store.Deliveries()[1].Decision.Choice)
}

func TestAnAuthenticatedOperatorCanAskToBeQuestionedAndFinishWithADraft(t *testing.T) {
	store := steeringtest.New().WithSession(aWaitingSession(fixedNow.Add(-time.Hour)))
	server := newSteeringServer(t, store)
	target := BasePath + "/steering/sessions/steering-review-1/question"

	asked := steeringRequest(t, server, http.MethodPost, target, `{"text":"Question me"}`)
	require.Equal(t, http.StatusOK, asked.Code, asked.Body.String())
	var conversation steeringSessionResource
	decodeResponse(t, asked, &conversation)
	require.Len(t, conversation.Messages, 2)
	require.NotEmpty(t, conversation.Messages[0].Author)
	require.Equal(t, "Which callers depend on the wrapped cause?", conversation.Messages[1].Text)

	finished := steeringRequest(t, server, http.MethodPost, target,
		`{"text":"That is enough","finish":true}`)
	require.Equal(t, http.StatusOK, finished.Code, finished.Body.String())
	decodeResponse(t, finished, &conversation)
	require.Equal(t, "Keep the retry and preserve the wrapped cause.", conversation.Guidance)
	require.Equal(t, 80, conversation.Tokens)
}

func TestASteeringDecisionObeysAuthenticationAndSameSiteRules(t *testing.T) {
	store := steeringtest.New().WithSession(aWaitingSession(fixedNow))
	server := newSteeringServer(t, store)
	target := BasePath + "/steering/sessions/steering-review-1/decision"

	unauthenticated := newRequest(http.MethodPost, target, strings.NewReader(`{"decision":"skip"}`))
	unauthenticated.Header.Set("Content-Type", "application/json")
	unauthenticated.Header.Set("Sec-Fetch-Site", "same-origin")
	unauthenticatedResponse := httptest.NewRecorder()
	server.ServeHTTP(unauthenticatedResponse, unauthenticated)
	requireProblem(t, unauthenticatedResponse, http.StatusUnauthorized, codeAuthenticationRequired)

	crossSite := steeringRequest(t, server, http.MethodPost, target, `{"decision":"skip"}`,
		func(request *http.Request) { request.Header.Set("Sec-Fetch-Site", "cross-site") })
	requireProblem(t, crossSite, http.StatusForbidden, codeCrossSiteRequest)

	session, err := store.Session(context.Background(), "steering-review-1")
	require.NoError(t, err)
	require.True(t, session.Waiting(), "a refused request must decide nothing")
}

func TestARejectedSteeringDecisionExplainsWhyNothingWasResumed(t *testing.T) {
	store := steeringtest.New().WithSession(aWaitingSession(fixedNow))
	server := newSteeringServer(t, store)

	response := steeringRequest(t, server, http.MethodPost,
		BasePath+"/steering/sessions/steering-review-1/decision", `{"decision":"guide"}`)

	problem := requireProblem(t, response, http.StatusBadRequest, codeInvalidRequest)
	require.Contains(t, problem.Detail, "needs guidance")
	require.Empty(t, store.Deliveries())
}
