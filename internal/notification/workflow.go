package notification

import (
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// notifyActivityOptions is the shared workflow-side delivery policy for the
// best-effort notification activity: a short timeout with a couple of retries.
// Centralizing it here keeps every workflow's notification behavior consistent.
func notifyActivityOptions(ctx workflow.Context) workflow.Context {
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
func NotifyBestEffort(ctx workflow.Context, n Notification) {
	opts := notifyActivityOptions(ctx)
	var a *Activity
	if err := workflow.ExecuteActivity(opts, a.Notify, n).Get(opts, nil); err != nil {
		workflow.GetLogger(ctx).Warn("could not send notification", "error", err)
	}
}

// NotifyFailureBestEffort notifies best-effort that a workflow failed, using err
// as the body. It is a no-op on success (nil err) and on continue-as-new, which
// is a control signal used to chain/loop runs rather than a failure. Delivery is
// best-effort via NotifyBestEffort, so a notification problem never masks the
// original error.
func NotifyFailureBestEffort(ctx workflow.Context, title string, err error) {
	if err == nil {
		return
	}
	var canErr *workflow.ContinueAsNewError
	if errors.As(err, &canErr) {
		return
	}
	NotifyBestEffort(ctx, Notification{Title: title, Body: err.Error()})
}
