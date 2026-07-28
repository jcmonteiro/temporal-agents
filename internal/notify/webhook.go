package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"temporal-agents/internal/notification"
)

// Webhook is a driven adapter that POSTs a notification as JSON to a configured
// URL. It implements notification.Notifier.
type Webhook struct {
	url    string
	client *http.Client
}

// NewWebhook returns a webhook notifier that POSTs to url.
func NewWebhook(url string) Webhook {
	return Webhook{url: url, client: &http.Client{Timeout: 10 * time.Second}}
}

// webhookPayload is the JSON body posted to the webhook URL.
type webhookPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// URL is a hyperlink to the relevant resource (e.g. the pull request). It is
	// omitted when the notification carries no link.
	URL string `json:"url,omitempty"`
}

// Notify POSTs n to the webhook URL as JSON and treats any non-2xx response as
// a failure. When n.WebhookBody is set it replaces n.Body in the payload, so
// the webhook can carry the agent-generated summary while other channels keep
// the plain body.
func (w Webhook) Notify(ctx context.Context, n notification.Notification) error {
	body, err := json.Marshal(webhookPayload{Title: n.Title, Body: webhookBody(n), URL: n.URL})
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", sanitizeURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("post webhook: %w", sanitizeURLError(err))
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// webhookBody returns the body to POST: the webhook-only override when set,
// otherwise the notification's plain body.
func webhookBody(n notification.Notification) string {
	if n.WebhookBody != "" {
		return n.WebhookBody
	}
	return n.Body
}

// sanitizeURLError strips the request URL from *url.Error values so that
// secret-bearing webhook paths or query parameters are not leaked into Temporal
// activity failures and workflow logs. The underlying cause is retained.
func sanitizeURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}
