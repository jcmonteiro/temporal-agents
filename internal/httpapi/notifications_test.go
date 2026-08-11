package httpapi

import (
	"context"
	"testing"

	"temporal-agents/internal/notification"
)

type notificationsStub struct{ items []notification.InboxItem }

func (s *notificationsStub) List(context.Context, string, int) ([]notification.InboxItem, error) {
	return s.items, nil
}
func (s *notificationsStub) Unread(_ context.Context, _ string) (int, error) {
	n := 0
	for _, i := range s.items {
		if !i.Read {
			n++
		}
	}
	return n, nil
}
func (s *notificationsStub) MarkRead(_ context.Context, _ string, id string) error {
	for n := range s.items {
		if s.items[n].ID == id {
			s.items[n].Read = true
		}
	}
	return nil
}
func (s *notificationsStub) ClearRead(context.Context, string) error {
	for n := range s.items {
		s.items[n].Read = false
	}
	return nil
}

func defaultNotifications() NotificationsView { return &notificationsStub{} }

func TestNotificationInboxCountMatchesUnreadItems(t *testing.T) {
	inbox := &notificationsStub{items: []notification.InboxItem{{ID: "one", Title: "Waiting"}, {ID: "two", Title: "Read", Read: true}}}
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.Notifications = inbox })
	response := request(t, server, "GET", BasePath+"/notifications", nil)
	if response.Code != 200 {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	var body notificationCollection
	decodeResponse(t, response, &body)
	if body.Unread != 1 || body.Count != 2 {
		t.Fatalf("inbox = %+v", body)
	}
}
