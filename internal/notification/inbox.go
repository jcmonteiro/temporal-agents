package notification

import (
	"context"
	"errors"
	"time"
)

// InboxItem is one durable notification as a principal reads it.
type InboxItem struct {
	ID        string
	Kind      string
	Title     string
	Body      string
	URL       string
	SessionID string
	CreatedAt time.Time
	Read      bool
}

// InboxStore owns durable notifications and per-principal read state. An empty
// recipient on a write is broadcast; a non-empty recipient is visible only there.
type InboxStore interface {
	Put(ctx context.Context, notification Notification) error
	List(ctx context.Context, principal string, limit int) ([]InboxItem, error)
	Unread(ctx context.Context, principal string) (int, error)
	MarkRead(ctx context.Context, principal, id string) error
	ClearRead(ctx context.Context, principal string) error
}

// Inbox is the application service used by the HTTP adapter.
type Inbox struct{ Store InboxStore }

var ErrInboxUnavailable = errors.New("the notification inbox is unavailable")

func (i *Inbox) List(ctx context.Context, principal string, limit int) ([]InboxItem, error) {
	return i.Store.List(ctx, principal, limit)
}
func (i *Inbox) Unread(ctx context.Context, principal string) (int, error) {
	return i.Store.Unread(ctx, principal)
}
func (i *Inbox) MarkRead(ctx context.Context, principal, id string) error {
	return i.Store.MarkRead(ctx, principal, id)
}
func (i *Inbox) ClearRead(ctx context.Context, principal string) error {
	return i.Store.ClearRead(ctx, principal)
}
