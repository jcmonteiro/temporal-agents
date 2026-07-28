// Package wfnotify holds the workflow-side, best-effort delivery helpers for
// the notification port. It depends on both the pure notification port (for the
// Notification and Activity types) and the Temporal SDK, so every workflow
// shares one delivery policy while the port package itself stays SDK-free.
package wfnotify

import (
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"temporal-agents/internal/notification"
)

// withNotifyOptions returns ctx with the shared workflow-side delivery policy
// for the best-effort notification activity applied: a short timeout with a
// couple of retries. Centralizing it here keeps every workflow's notification
// behavior consistent.
func withNotifyOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
}

// NotifyBestEffort schedules the Notify activity for n and swallows (after
// logging) any delivery failure, so a notification problem never fails or masks
// the workflow that requested it. It is the single workflow-side entry point
// for delivering a notification, so the retry/timeout policy lives in one
// place.
func NotifyBestEffort(ctx workflow.Context, n notification.Notification) {
	opts := withNotifyOptions(ctx)
	var a *notification.Activity
	if err := workflow.ExecuteActivity(opts, a.Notify, n).Get(opts, nil); err != nil {
		workflow.GetLogger(ctx).Warn("could not send notification", "error", err)
	}
}

// NotifyFailureBestEffort notifies best-effort that a workflow failed, using err
// as the body. It is a no-op on success (nil err) and on continue-as-new, which
// is a control signal used to chain/loop runs rather than a failure. Delivery is
// best-effort via NotifyBestEffort, so a notification problem never masks the
// original error.
//
// The notification is scheduled on a disconnected context for every failure,
// not only cancellation. The motivating case is a workflow that failed because
// it was cancelled — the case where a heads-up is most useful — since
// scheduling on the (now cancelled) workflow context would fail immediately and
// silently drop the failure notification. Because the disconnected context is
// used unconditionally, every failure notify (cancelled or not) pays the
// activity's own timeout and retries, which can delay workflow close by up to
// the full delivery budget; that delay is accepted for a best-effort heads-up.
//
// The body is the workflow error verbatim, so network adapters (e.g. the
// webhook notifier) transmit it as-is — the adapter only sanitizes transport
// errors, not this body. Current workflow errors carry no secrets; if an
// activity error ever embeds a token or signed URL, sanitize it at its source
// before it reaches here.
func NotifyFailureBestEffort(ctx workflow.Context, title string, err error) {
	NotifyFailureBestEffortWith(ctx, title, err, nil)
}

// NotifyFailureBestEffortWith behaves exactly like NotifyFailureBestEffort but
// lets the caller enrich the notification before delivery — for example to
// attach a webhook-only body — without re-implementing the shared failure
// policy (the nil/continue-as-new exclusions and the disconnected delivery
// context). enrich, when non-nil, is invoked with the disconnected delivery
// context and the base failure notification, and returns the notification to
// deliver; running it on the disconnected context lets it schedule its own
// activities (e.g. a summary step) that must also survive a cancelled workflow
// context. A nil enrich delivers the plain failure notification.
func NotifyFailureBestEffortWith(ctx workflow.Context, title string, err error, enrich func(ctx workflow.Context, n notification.Notification) notification.Notification) {
	if err == nil {
		return
	}
	var canErr *workflow.ContinueAsNewError
	if errors.As(err, &canErr) {
		return
	}
	dctx, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	n := notification.Notification{Title: title, Body: err.Error()}
	if enrich != nil {
		n = enrich(dctx, n)
	}
	NotifyBestEffort(dctx, n)
}
