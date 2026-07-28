// Package notification defines the notification port shared by the workflows
// and the thin activity that drives it. The port types (Notification, Notifier,
// Activity) depend only on the standard library; concrete adapters (see the
// notify package) implement Notifier from the edges, keeping the cores
// decoupled from delivery details. It also owns the workflow-side best-effort
// delivery helpers (see workflow.go), which depend on the Temporal SDK so every
// workflow shares one delivery policy.
package notification

import (
	"context"
	"fmt"
)

// Notification is a completion message handed to a Notifier.
type Notification struct {
	// Title is the short headline (e.g. the notification's bold first line).
	Title string
	// Body is the human-readable detail, typically the workflow's summary.
	Body string
	// URL, when set, is a hyperlink to the relevant resource (e.g. the pull
	// request a review chain operated on). It is empty when there is no such
	// resource. Adapters render it however their channel allows.
	URL string
}

// Notifier is the port for delivering a Notification to the outside world.
// Sending is best-effort; implementations should tolerate duplicate deliveries
// because the driving activity may be retried.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// Activity drives the Notifier as a Temporal activity. It is registered with
// the worker once and referenced by every workflow that notifies. A nil
// Notifier makes Notify a no-op, which is the natural state when no notifier is
// enabled at worker start.
type Activity struct {
	Notifier Notifier
}

// Notify delivers n via the configured Notifier, or does nothing when none is
// configured.
func (a *Activity) Notify(ctx context.Context, n Notification) error {
	if a.Notifier == nil {
		return nil
	}
	if err := a.Notifier.Notify(ctx, n); err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	return nil
}
