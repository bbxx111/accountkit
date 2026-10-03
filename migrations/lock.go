package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4/database"
	"github.com/jackc/pgx/v5"
)

// lockMigration covers schema and migration-table creation, before the engine's own lock.
func lockMigration(ctx context.Context, cfg *pgx.ConnConfig, schema string) (func(), error) {
	if cfg == nil {
		return nil, fmt.Errorf("migrations: connection config is required")
	}
	waiting, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(waiting, cfg.Copy())
	if err != nil {
		return nil, fmt.Errorf("migrations: lock connection: %w", err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			closing, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = conn.Close(closing)
		})
	}
	var database string
	if err := conn.QueryRow(waiting, "SELECT current_database()").Scan(&database); err != nil {
		release()
		return nil, fmt.Errorf("migrations: lock database: %w", err)
	}
	sum := sha256.Sum256([]byte("accountkit:migration:init:" + database + "\x00" + schema))
	key := int64(binary.BigEndian.Uint64(sum[:8]))
	if _, err := conn.Exec(waiting, "SELECT pg_advisory_lock($1)", key); err != nil {
		release()
		if waiting.Err() != nil {
			return nil, fmt.Errorf("migrations: lock wait for schema %s: %w", schema, waiting.Err())
		}
		return nil, fmt.Errorf("migrations: lock schema %s: %w", schema, err)
	}
	if err := waiting.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// Keep one extra acquisition until the dedicated migration connection closes.
// The engine balances its own nested acquisitions; no other pool uses this DB.
func lockLegacyEngine(ctx context.Context, conn *pgx.Conn, schema string) (err error) {
	waiting, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	defer func() {
		if err != nil {
			closing, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer closeCancel()
			_ = conn.Close(closing)
		}
	}()
	var dbName string
	if err = conn.QueryRow(waiting, "SELECT current_database()").Scan(&dbName); err != nil {
		return err
	}
	key, err := database.GenerateAdvisoryLockId(dbName, schema, "schema_migrations")
	if err != nil {
		return err
	}
	if _, err = conn.Exec(waiting, "SELECT pg_advisory_lock($1)", key); err != nil {
		if waiting.Err() != nil {
			return fmt.Errorf("migrations: engine lock wait for schema %s: %w", schema, waiting.Err())
		}
		return fmt.Errorf("migrations: engine lock for schema %s: %w", schema, err)
	}
	return waiting.Err()
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
