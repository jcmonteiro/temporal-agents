package notify

import (
	"testing"

	"temporal-agents/internal/notification"
)

func TestDesktopBodyAppendsURL(t *testing.T) {
	tests := []struct {
		name string
		n    notification.Notification
		want string
	}{
		{
			name: "body and url",
			n:    notification.Notification{Body: "3 commits", URL: "https://github.com/acme/widgets/pull/7"},
			want: "3 commits\nhttps://github.com/acme/widgets/pull/7",
		},
		{
			name: "url only",
			n:    notification.Notification{URL: "https://github.com/acme/widgets/pull/7"},
			want: "https://github.com/acme/widgets/pull/7",
		},
		{
			name: "no url leaves body unchanged",
			n:    notification.Notification{Body: "3 commits"},
			want: "3 commits",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := desktopBody(tt.n); got != tt.want {
				t.Errorf("desktopBody = %q, want %q", got, tt.want)
			}
		})
	}
}
