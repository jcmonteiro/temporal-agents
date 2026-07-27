// Package notify holds driven adapters that implement the codereview.Notifier
// port. Two concrete notifiers are provided—a macOS desktop notifier over
// osascript and an HTTP webhook—plus a Multi adapter that fans a notification
// out to several notifiers.
package notify

import (
	"context"
	"errors"

	"temporal-agents/internal/codereview"
)

// Multi fans a notification out to every wrapped notifier, delivering to all of
// them even if some fail and joining any errors. An empty Multi is a no-op,
// which is the natural result when no notifier is enabled at worker start.
type Multi []codereview.Notifier

// Notify delivers n to each wrapped notifier, collecting errors so one failing
// channel does not stop the others.
func (m Multi) Notify(ctx context.Context, n codereview.Notification) error {
	var errs []error
	for _, notifier := range m {
		if notifier == nil {
			continue
		}
		if err := notifier.Notify(ctx, n); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
