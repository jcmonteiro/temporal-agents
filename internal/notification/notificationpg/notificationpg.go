package notificationpg

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"temporal-agents/internal/notification"
	"temporal-agents/internal/pgmigrate"
	"temporal-agents/internal/schema"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const (
	migrationDir       = "migrations"
	migrationNamespace = "notifications"
)

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("no database DSN configured")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("configure the notification store: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to the notification store: %w", err)
	}
	return &Store{pool: pool}, nil
}
func (s *Store) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}
func (s *Store) Migrate(ctx context.Context) error {
	return pgmigrate.Apply(ctx, s.pool, migrationFS, migrationDir, migrationNamespace)
}
func (s *Store) SchemaState(ctx context.Context) (schema.State, error) {
	return pgmigrate.Inspect(ctx, s.pool, migrationFS, migrationDir, migrationNamespace)
}

func (s *Store) Notify(ctx context.Context, n notification.Notification) error {
	if n.ID == "" {
		return nil
	}
	return s.Put(ctx, n)
}

func (s *Store) Put(ctx context.Context, n notification.Notification) error {
	at := n.CreatedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO notifications (id, kind, recipient, title, body, url, session_id, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (id) DO NOTHING`,
		n.ID, n.Kind, n.Recipient, n.Title, n.Body, n.URL, n.SessionID, at)
	return err
}

func (s *Store) List(ctx context.Context, principal string, limit int) ([]notification.InboxItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
SELECT n.id,n.kind,n.title,n.body,n.url,n.session_id,n.created_at,(r.notification_id IS NOT NULL)
FROM notifications n LEFT JOIN notification_reads r
  ON r.notification_id=n.id AND r.principal=$1
WHERE n.recipient='' OR n.recipient=$1
ORDER BY n.created_at DESC,n.id DESC LIMIT $2`, principal, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (notification.InboxItem, error) {
		var item notification.InboxItem
		err := row.Scan(&item.ID, &item.Kind, &item.Title, &item.Body, &item.URL, &item.SessionID, &item.CreatedAt, &item.Read)
		return item, err
	})
}

func (s *Store) Unread(ctx context.Context, principal string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
SELECT count(*) FROM notifications n WHERE (n.recipient='' OR n.recipient=$1)
AND NOT EXISTS (SELECT 1 FROM notification_reads r WHERE r.notification_id=n.id AND r.principal=$1)`, principal).Scan(&count)
	return count, err
}

func (s *Store) MarkRead(ctx context.Context, principal, id string) error {
	tag, err := s.pool.Exec(ctx, `
INSERT INTO notification_reads (notification_id,principal,read_at)
SELECT id,$1,now() FROM notifications WHERE id=$2 AND (recipient='' OR recipient=$1)
ON CONFLICT DO NOTHING`, principal, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		_ = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notifications WHERE id=$1)`, id).Scan(&exists)
		if !exists {
			return errors.New("no such notification")
		}
	}
	return nil
}

func (s *Store) ClearRead(ctx context.Context, principal string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM notification_reads WHERE principal=$1`, principal)
	return err
}

var _ notification.Notifier = (*Store)(nil)
var _ notification.InboxStore = (*Store)(nil)
