package notificationpg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/notification"
	"temporal-agents/internal/pgtest"
)

func TestAddressingAndReadStateArePerPrincipal(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, pgtest.NewDatabase(t))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	require.NoError(t, store.Migrate(ctx))
	now := time.Now()
	for _, item := range []notification.Notification{
		{ID: "broadcast", Kind: "steering", Title: "Broadcast", CreatedAt: now},
		{ID: "for-ada", Kind: "steering", Recipient: "ada", Title: "Ada", CreatedAt: now.Add(time.Second)},
		{ID: "for-grace", Kind: "steering", Recipient: "grace", Title: "Grace", CreatedAt: now.Add(2 * time.Second)},
	} {
		require.NoError(t, store.Put(ctx, item))
	}

	ada, err := store.List(ctx, "ada", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"Ada", "Broadcast"}, []string{ada[0].Title, ada[1].Title})
	grace, err := store.List(ctx, "grace", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"Grace", "Broadcast"}, []string{grace[0].Title, grace[1].Title})

	require.NoError(t, store.MarkRead(ctx, "ada", "broadcast"))
	adaUnread, err := store.Unread(ctx, "ada")
	require.NoError(t, err)
	graceUnread, err := store.Unread(ctx, "grace")
	require.NoError(t, err)
	require.Equal(t, 1, adaUnread)
	require.Equal(t, 2, graceUnread, "one principal reading a broadcast must not clear it for another")
}
