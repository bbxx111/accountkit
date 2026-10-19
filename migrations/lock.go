package migrations

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bbxx111/accountkit/internal/migrationlock"
	"github.com/jackc/pgx/v5"
)

func lockMigration(ctx context.Context, cfg *pgx.ConnConfig, schema string) (func(), error) {
	return migrationlock.Initialization(ctx, cfg, schema)
}
func lockLegacyEngine(ctx context.Context, conn *pgx.Conn, schema string) error {
	return migrationlock.Engine(ctx, conn, schema)
}

// WithInstance can fail after borrowing a sql.Conn without returning it.
// Own the physical connections too, so every exit releases session locks.
// The mutex also covers database/sql's background connection establishment.
type migrationConnections struct {
	mu     sync.Mutex
	closed bool
	conns  []*pgx.Conn
}

func (c *migrationConnections) add(conn *pgx.Conn) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		closeMigrationConnection(conn)
		return fmt.Errorf("migrations: connection owner closed")
	}
	c.conns = append(c.conns, conn)
	return nil
}
func (c *migrationConnections) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for _, conn := range c.conns {
		closeMigrationConnection(conn)
	}
	c.conns = nil
}
func closeMigrationConnection(conn *pgx.Conn) {
	closing, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Close(closing)
}
