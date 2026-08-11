package steering

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"temporal-agents/internal/place"
)

// A session is what an operator actually answers: the round that is waiting, the
// material the decision is about, the conversation that may have produced the
// guidance, and the decision that ended it.
//
// It is durable in a store of its own for two reasons the implementation brief
// states as constraints. The conversation is authoritative in the durable store,
// because streaming partial text into orchestration history would bloat it and make
// replay expensive; and the decision must be recorded before the loop is resumed, so
// a decision that cannot be written fails in front of the operator instead of being
// reported as accepted.
//
// The types here are the vocabulary of that store, and they name nothing outside
// this context: a session references the loop that is waiting by its identity, never
// by a foreign key into the execution record.

// ErrNoSuchSession reports a session nobody opened, or one whose store has forgotten
// it. It is separate from a store outage so a reader can tell "there is nothing to
// answer" from "the answer could not be reached".
var ErrNoSuchSession = errors.New("no such steering session")

// ErrUnavailable reports that the durable store could not be reached. A decision
// that produced it is not made: the operator is told so, and may send it again.
var ErrUnavailable = errors.New("the steering store could not be reached")

// ErrInvalidMessage marks a turn of the conversation that cannot be appended as it
// stands. Like a refused decision, it always names what is wrong.
var ErrInvalidMessage = errors.New("invalid conversation message")

// State is where a session stands. A session waits for a human, and then it has
// been answered — or the work it was waiting for is gone, which is the one other
// way a wait can end.
type State string

const (
	// StateWaiting is a session that has not been answered yet.
	StateWaiting State = "waiting"
	// StateDecided is a session an operator has answered. The decision it recorded
	// is final: the first one wins, and the loop acted on it.
	StateDecided State = "decided"
	// StateAbandoned is a session whose loop ended before anybody answered — it was
	// cancelled, or it failed. It is recorded rather than left waiting, because a
	// round nobody can answer must not keep asking.
	StateAbandoned State = "abandoned"
)

// Role says who produced one message of the conversation.
type Role string

const (
	// RoleOperator is a message an operator wrote.
	RoleOperator Role = "operator"
	// RoleAgent is a message the questioning agent produced. It costs tokens, which
	// is why a message carries a cost at all.
	RoleAgent Role = "agent"
)

// Session is one waiting round, as the durable store holds it.
type Session struct {
	// ID is the session's identity, and it is the identity of the orchestrated unit
	// that waits (see SessionID). It is stable for the session's whole life, which
	// is what lets a conversation be keyed on it.
	ID string
	// ItemID is the run that is waiting — the work an operator sees on the overview.
	// It is carried as a value rather than as a reference into the execution record:
	// the two are separate contexts, and neither may block the other from changing.
	ItemID string
	// Round is which pause point this session is.
	Round Round
	// Material is what the decision is about: the review's findings, or the
	// unresolved comments, as the agent would have received them. It is stored here
	// because it is what an operator has to read to decide, and because nothing else
	// keeps it.
	Material string
	// Place is where the paused work runs.
	Place place.Facts
	// Guidance is the text the operator is composing, kept editable until the
	// decision is sent. It is the session's only artifact.
	Guidance string
	// OpenedAt is when the round started waiting, so an interface can say since when
	// — an unbounded wait that does not say how long it has been waiting is not
	// prominent, it is merely present.
	OpenedAt time.Time
	// State is where the session stands.
	State State
	// Decision is what an operator decided, and the zero value while none has.
	Decision Decision
	// DecidedAt is when the decision that won was recorded.
	DecidedAt time.Time
}

// Waiting reports whether the session is still waiting for an operator.
func (s Session) Waiting() bool { return s.State == StateWaiting }

// Message is one turn of the conversation that may produce the guidance.
//
// Messages are append-only and carry a per-session sequence that only ever
// increases, so a reader that has seen sequence n asks for what came after n and
// cannot miss a turn or replay one. That is what makes the stream resumable.
type Message struct {
	// SessionID is the session this message belongs to.
	SessionID string
	// Sequence is this message's position in the session, counted from 1 and never
	// reused.
	Sequence int64
	// Role says who produced it.
	Role Role
	// Author is the principal who wrote it, and is empty for the agent's own turns.
	// Every contribution records who made it, because any signed-in operator may
	// answer.
	Author string
	// Text is what was said.
	Text string
	// Tokens is what this turn cost. Only the agent's turns cost anything, and the
	// cost is visible while the conversation grows because it is operator-driven.
	Tokens int
	// At is when it was recorded.
	At time.Time
}

// Event is a small durable notification that tells a hub reader to refetch its
// source of truth. It never carries session material or list data.
type Event struct {
	Sequence  int64
	Type      string
	SessionID string
	ItemID    string
	At        time.Time
}

// Conversation is one session with everything an operator needs to decide it: what
// the decision is about, the guidance so far, the turns that produced it, what it
// has cost, and who has taken part.
type Conversation struct {
	// Session is the waiting round itself.
	Session Session
	// Messages are the turns so far, in sequence order.
	Messages []Message
}

// Tokens is what the session has cost so far. A session starts free — questioning
// happens only when it is asked for — so this is zero for a session an operator
// simply typed into.
func (c Conversation) Tokens() int {
	total := 0
	for _, message := range c.Messages {
		total += message.Tokens
	}
	return total
}

// Contributors are the principals who have taken part, in the order they first did,
// including the one who decided. It answers "who has been involved" without a
// consumer having to walk the conversation itself.
func (c Conversation) Contributors() []string {
	seen := map[string]bool{}
	contributors := make([]string, 0, len(c.Messages)+1)
	add := func(principal string) {
		principal = strings.TrimSpace(principal)
		if principal == "" || seen[principal] {
			return
		}
		seen[principal] = true
		contributors = append(contributors, principal)
	}
	for _, message := range c.Messages {
		add(message.Author)
	}
	add(c.Session.Decision.Principal)
	return contributors
}

// Validate checks a message before it is appended, so a turn that could not be read
// back is refused where it was written.
func (m Message) Validate() error {
	switch m.Role {
	case RoleOperator, RoleAgent:
	default:
		return fmt.Errorf("%w: %q is not a role in this conversation", ErrInvalidMessage, m.Role)
	}
	if strings.TrimSpace(m.SessionID) == "" {
		return fmt.Errorf("%w: a message belongs to a session", ErrInvalidMessage)
	}
	if strings.TrimSpace(m.Text) == "" {
		return fmt.Errorf("%w: an empty turn is not a message", ErrInvalidMessage)
	}
	return nil
}
