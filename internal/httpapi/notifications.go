package httpapi

import (
	"context"
	"net/http"

	"temporal-agents/internal/notification"
)

type NotificationsView interface {
	List(ctx context.Context, principal string, limit int) ([]notification.InboxItem, error)
	Unread(ctx context.Context, principal string) (int, error)
	MarkRead(ctx context.Context, principal, id string) error
	ClearRead(ctx context.Context, principal string) error
}

type notificationResource struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	URL       string  `json:"url,omitempty"`
	SessionID string  `json:"sessionId,omitempty"`
	CreatedAt *string `json:"createdAt"`
	Read      bool    `json:"read"`
}

type notificationCollection struct {
	Items  []notificationResource `json:"items"`
	Count  int                    `json:"count"`
	Limit  int                    `json:"limit"`
	Unread int                    `json:"unread"`
}

func requestPrincipal(r *http.Request) string {
	if principal, ok := PrincipalFrom(r.Context()); ok {
		return principal.ID()
	}
	return "local-operator"
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	limit, ok := s.limitParam(w, r)
	if !ok {
		return
	}
	principal := requestPrincipal(r)
	items, err := s.notifications.List(r.Context(), principal, limit)
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	unread, err := s.notifications.Unread(r.Context(), principal)
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	resources := make([]notificationResource, 0, len(items))
	for _, item := range items {
		resources = append(resources, notificationResource{
			ID: item.ID, Kind: item.Kind, Title: item.Title, Body: item.Body, URL: item.URL,
			SessionID: item.SessionID, CreatedAt: timestamp(item.CreatedAt), Read: item.Read,
		})
	}
	s.writeJSON(w, r, http.StatusOK, modelNotificationCollection,
		notificationCollection{Items: resources, Count: len(resources), Limit: limit, Unread: unread})
}

func (s *Server) handleNotificationRead(w http.ResponseWriter, r *http.Request) {
	if err := s.notifications.MarkRead(r.Context(), requestPrincipal(r), r.PathValue("id")); err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNotificationClearRead(w http.ResponseWriter, r *http.Request) {
	if err := s.notifications.ClearRead(r.Context(), requestPrincipal(r)); err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
