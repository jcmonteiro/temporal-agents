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
// The notification is scheduled on a disconnected context so a workflow that
// failed because it was cancelled — the case where a heads-up is most useful —
// still notifies. Scheduling on the (now cancelled) workflow context would fail
// immediately and silently drop the failure notification.
//
// The body is the workflow error verbatim, so network adapters (e.g. the
// webhook notifier) transmit it as-is — the adapter only sanitizes transport
// errors, not this body. Current workflow errors carry no secrets; if an
// activity error ever embeds a token or signed URL, sanitize it at its source
// before it reaches here.
func NotifyFailureBestEffort(ctx workflow.Context, title string, err error) {
	if err == nil {
		return
	}
	var canErr *workflow.ContinueAsNewError
	if errors.As(err, &canErr) {
		return
	}
	dctx, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	NotifyBestEffort(dctx, notification.Notification{Title: title, Body: err.Error()})
}
