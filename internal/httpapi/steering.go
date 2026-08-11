package httpapi

import (
	"context"
	"errors"
	"net/http"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/steering"
)

// A waiting round is the one thing in this API that a person is expected to answer,
// so it is published as a resource of its own: the rounds waiting, one of them with
// everything needed to decide it, and the decision itself as a write.
//
// The decision is the second mutation this API offers, and it is subject to the
// same rules as the first: a credential, a same-site request, a bounded and strictly
// decoded body. It answers 200 rather than 201 because it creates nothing — it
// answers a question that already exists — and a repeat is answered with the
// decision that won, because two tabs and a retried request are ordinary and neither
// may start a second implementation pass.

// SteeringView is the driving port for the human-in-the-loop pause: what is waiting,
// what one waiting round is about, and the decision that ends it.
//
// It is a port of its own, beside the work view rather than inside it, for the same
// reason starting work is: everything the work view offers reads what happened, while
// this resumes a loop on the operator's machine. A deployment that publishes no
// steering surface serves no steering resources at all.
//
// *steering.Service implements it.
type SteeringView interface {
	// Waiting returns the rounds currently waiting for an operator, oldest first.
	Waiting(ctx context.Context) ([]steering.Session, error)
	// Conversation returns one session with its material, guidance, turns and cost.
	Conversation(ctx context.Context, id string) (steering.Conversation, error)
	// Decide records a decision and resumes the round it was waiting on. A repeat
	// returns the decision that was recorded.
	Decide(ctx context.Context, id string, decision steering.Decision) (steering.Session, error)
}

// steeringSessionResource is one waiting round as the API publishes it.
type steeringSessionResource struct {
	// ID is the session's identity, which is what a decision is addressed to.
	ID string `json:"id"`
	// ItemID is the run that is waiting, so a consumer can go from the question back
	// to the work it is about.
	ItemID string `json:"itemId"`
	// Round says which pause point it is ("local-review", "remote-comments").
	Round string `json:"round"`
	// State is "waiting", "decided" or "abandoned".
	State string `json:"state"`
	// WaitingSince is when the round started waiting, in UTC. The wait is unbounded,
	// so how long it has been waiting is part of what makes it prominent.
	WaitingSince *string `json:"waitingSince"`
	// LocationID references the place the paused work runs in.
	LocationID string `json:"locationId"`
	// Material is what the decision is about, present only in the single-session
	// representation: a list of waiting rounds must not carry a review each.
	Material string `json:"material,omitempty"`
	// Guidance is the text as it stands, present only in the single-session
	// representation.
	Guidance string `json:"guidance,omitempty"`
	// Decision is what was decided ("guide", "skip", "stop"), absent while waiting.
	Decision string `json:"decision,omitempty"`
	// DecidedAt is when the decision that won was recorded, in UTC.
	DecidedAt *string `json:"decidedAt,omitempty"`
	// DecidedBy is the principal who decided. Any signed-in operator may answer, so
	// this says who did, never who was allowed to.
	DecidedBy string `json:"decidedBy,omitempty"`
	// Tokens is what the session has cost so far. It is zero until the operator asks
	// to be questioned, and it grows as they do.
	Tokens int `json:"tokens,omitempty"`
	// Contributors are the principals who have taken part, in the order they first
	// did.
	Contributors []string `json:"contributors,omitempty"`
	// Messages is the conversation so far, present only in the single-session
	// representation.
	Messages []steeringMessageResource `json:"messages,omitempty"`
	// Locations is the registry this resource's reference resolves against, present
	// only in the single-session representation, which has no envelope to carry it.
	Locations []locationResource `json:"locations,omitempty"`
}

// steeringMessageResource is one turn of the conversation.
type steeringMessageResource struct {
	// Sequence is the turn's position, counted from 1. A consumer resumes a stream
	// by asking for what came after the sequence it has.
	Sequence int64 `json:"sequence"`
	// Role says who produced it: "operator" or "agent".
	Role string `json:"role"`
	// Author is the principal who wrote it, absent for the agent's own turns.
	Author string `json:"author,omitempty"`
	// Text is what was said.
	Text string `json:"text"`
	// Tokens is what the turn cost.
	Tokens int `json:"tokens,omitempty"`
	// At is when it was recorded, in UTC.
	At *string `json:"at"`
}

// steeringDecisionRequest is what an operator sends to answer a waiting round.
type steeringDecisionRequest struct {
	// Decision is one of "guide", "skip" or "stop". Guiding carries the text;
	// skipping is how an operator proceeds without any, so empty text never has to
	// mean it.
	Decision string `json:"decision"`
	// Guidance is the text handed to the agent, required for "guide" and refused for
	// the other two.
	Guidance string `json:"guidance,omitempty"`
}

// steeringSessionFrom projects a session onto its representation. withDetail selects
// the single-session representation, which carries what a person reads to decide.
func steeringSessionFrom(session steering.Session, withDetail bool) (steeringSessionResource, agenthub.Location, error) {
	location, err := agenthub.RecordedPlace{
		Directory:  session.Place.Directory,
		Repository: session.Place.Repository,
	}.Location()
	if err != nil {
		return steeringSessionResource{}, agenthub.Location{}, err
	}
	resource := steeringSessionResource{
		ID:           session.ID,
		ItemID:       session.ItemID,
		Round:        string(session.Round),
		State:        string(session.State),
		WaitingSince: timestamp(session.OpenedAt),
		LocationID:   location.ID(),
		Decision:     string(session.Decision.Choice),
		DecidedAt:    timestamp(session.DecidedAt),
		DecidedBy:    session.Decision.Principal,
	}
	if withDetail {
		resource.Material = session.Material
		resource.Guidance = session.Guidance
	}
	return resource, location, nil
}

// handleSteeringSessions answers the rounds waiting for an operator.
func (s *Server) handleSteeringSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.steering.Waiting(r.Context())
	if err != nil {
		s.writeSteeringProblem(w, r, err)
		return
	}
	items := make([]steeringSessionResource, 0, len(sessions))
	locations := make([]agenthub.Location, 0, len(sessions))
	for _, session := range sessions {
		resource, location, err := steeringSessionFrom(session, false)
		if err != nil {
			s.writeSteeringProblem(w, r, err)
			return
		}
		items = append(items, resource)
		locations = append(locations, location)
	}
	s.writeJSON(w, r, http.StatusOK, modelSteeringSessionCollection,
		newLocatedCollection(items, len(items), agenthub.NewLocationRegistry(locations...)))
}

// handleSteeringSession answers one waiting round with everything needed to decide
// it: what it is about, the guidance so far, the conversation, what it has cost, and
// who has taken part.
func (s *Server) handleSteeringSession(w http.ResponseWriter, r *http.Request) {
	conversation, err := s.steering.Conversation(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeSteeringProblem(w, r, err)
		return
	}
	resource, err := steeringConversationFrom(conversation)
	if err != nil {
		s.writeSteeringProblem(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, modelSteeringSession, resource)
}

// steeringConversationFrom projects one session and its turns onto the single-session
// representation.
func steeringConversationFrom(conversation steering.Conversation) (steeringSessionResource, error) {
	resource, location, err := steeringSessionFrom(conversation.Session, true)
	if err != nil {
		return steeringSessionResource{}, err
	}
	resource.Tokens = conversation.Tokens()
	resource.Contributors = conversation.Contributors()
	for _, message := range conversation.Messages {
		resource.Messages = append(resource.Messages, steeringMessageResource{
			Sequence: message.Sequence,
			Role:     string(message.Role),
			Author:   message.Author,
			Text:     message.Text,
			Tokens:   message.Tokens,
			At:       timestamp(message.At),
		})
	}
	resource.Locations = locationsFrom(agenthub.NewLocationRegistry(location))
	return resource, nil
}

// handleSteeringDecision answers a waiting round.
//
// It answers 200 with the session as it stands: a decision creates no resource, and
// a repeat is answered with the decision that was recorded rather than refused —
// reporting a conflict where the caller got exactly what it asked for would be a
// refusal of nothing.
func (s *Server) handleSteeringDecision(w http.ResponseWriter, r *http.Request) {
	var request steeringDecisionRequest
	if !s.decodeJSONBody(w, r, &request) {
		return
	}
	by := ""
	if principal, ok := PrincipalFrom(r.Context()); ok {
		by = principal.ID()
	}
	session, err := s.steering.Decide(r.Context(), r.PathValue("id"), steering.Decision{
		Choice:    steering.Choice(request.Decision),
		Guidance:  request.Guidance,
		Principal: by,
	})
	if err != nil {
		s.writeSteeringProblem(w, r, err)
		return
	}
	resource, _, err := steeringSessionFrom(session, true)
	if err != nil {
		s.writeSteeringProblem(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, modelSteeringSession, resource)
}

// writeSteeringProblem maps a failure from the steering context onto its problem
// document. A refused decision carries the core's own explanation, because the
// operator has to know which of the three decisions to send instead.
func (s *Server) writeSteeringProblem(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, steering.ErrNoSuchSession):
		s.writeProblem(w, r, codeNotFound, "no such steering session")
	case errors.Is(err, steering.ErrInvalidDecision):
		s.writeProblem(w, r, codeInvalidRequest, err.Error())
	case errors.Is(err, steering.ErrUnavailable):
		// The cause names a dependency and may carry a driver's message, so it goes to
		// the log while the consumer is told what to do about it: nothing was decided,
		// and the decision may be sent again.
		s.logger.Error("a steering decision could not be completed",
			"requestId", requestIDFrom(r.Context()), "path", r.URL.EscapedPath(), "error", err.Error())
		s.writeProblem(w, r, codeDependencyUnavailable,
			"the decision was not completed and nothing was resumed; send it again")
	default:
		s.writeServiceProblem(w, r, err)
	}
}
