// Package workoverview defines the application port used to read active work.
package workoverview

import "context"

// Kind is the display type of one top-level work item.
type Kind string

const (
	KindFleet     Kind = "fleet"
	KindRun       Kind = "run"
	KindDevelop   Kind = "develop"
	KindReview    Kind = "review"
	KindPilot     Kind = "pilot"
	KindFleetPlan Kind = "fleet-plan"
	KindSchedule  Kind = "schedule"
)

// ValidKind reports whether kind is part of the application contract.
func ValidKind(kind Kind) bool {
	switch kind {
	case KindFleet, KindRun, KindDevelop, KindReview, KindPilot, KindFleetPlan, KindSchedule:
		return true
	default:
		return false
	}
}

// Status is the status vocabulary used by the work overview.
type Status string

const (
	StatusTodo         Status = "todo"
	StatusInProgress   Status = "in-progress"
	StatusPaused       Status = "paused"
	StatusWaitingInput Status = "waiting-input"
	StatusWaiting      Status = "waiting"
	StatusDone         Status = "done"
	StatusFailed       Status = "failed"
)

// ValidStatus reports whether status is part of the application contract.
func ValidStatus(status Status) bool {
	switch status {
	case StatusTodo, StatusInProgress, StatusPaused, StatusWaitingInput, StatusWaiting, StatusDone, StatusFailed:
		return true
	default:
		return false
	}
}

// Item is one top-level work item. Running is execution liveness and is separate
// from Status, which can aggregate child outcomes.
type Item struct {
	ID      string
	Kind    Kind
	Status  Status
	Running bool
}

// Reader is the driven port used by commands that show active work.
type Reader interface {
	Overview(context.Context) ([]Item, error)
}
