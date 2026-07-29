package main

// R2 (realistic-stand): real Postgres client (pgx) + migration runner.
//
// DATABASE_URL empty  => DB disabled, order routes fall back to the in-memory map
//                        (so local/unit runs need no Postgres).
// DATABASE_URL set     => real pgxpool; migrations applied on startup; the order
//                        business path does a real write+read; /readyz SELECT 1s.
//
// Migrations are embedded in the binary (//go:embed) so they ship in the tiny
// distroless image. A failing migration is NOT swallowed — runMigrations returns
// an error, main log.Fatal's, the process exits non-zero, and the deploy breaks
// (the modeled risk).

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

var pool *pgxpool.Pool

func dbEnabled() bool {
	return strings.TrimSpace(os.Getenv("DATABASE_URL")) != ""
}

// initDB opens the pool. No-op (nil pool) when DATABASE_URL is empty.
func initDB(ctx context.Context) error {
	if !dbEnabled() {
		log.Printf("orders-service: DATABASE_URL not set — DB disabled, using in-memory store")
		return nil
	}
	p, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	pool = p
	return nil
}

// dbHealthy is the cheap readiness check: SELECT 1 with a ~500ms cap so probes
// never hammer Postgres. DB disabled => true (not a blocker). Error => false.
func dbHealthy(ctx context.Context) bool {
	if pool == nil {
		return true
	}
	c, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	var one int
	return pool.QueryRow(c, "SELECT 1").Scan(&one) == nil
}

// runMigrations applies every embedded migrations/*.sql in lexical order, each in
// its own transaction, recording applied files in schema_migrations so re-runs
// skip them. Returns an error on the first failing migration => exit non-zero.
func runMigrations(ctx context.Context) error {
	if pool == nil {
		return nil
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		filename text primary key,
		applied_at timestamptz not null default now()
	)`); err != nil {
		return err
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, f := range files {
		var exists bool
		if err := pool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename=$1)", f).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile("migrations/" + f)
		if err != nil {
			return err
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s failed: %w", f, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (filename) VALUES ($1)", f); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s record failed: %w", f, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migration %s commit failed: %w", f, err)
		}
		log.Printf("orders-service: applied migration %s", f)
	}
	return nil
}

// dbCreateOrder does a real INSERT then SELECT it back against the orders table.
func dbCreateOrder(ctx context.Context, item string, qty int) (*Order, error) {
	id := fmt.Sprintf("ord_%d", time.Now().UnixNano())
	if _, err := pool.Exec(ctx,
		"INSERT INTO orders (id, item, qty, status) VALUES ($1,$2,$3,'created')", id, item, qty); err != nil {
		return nil, err
	}
	o := &Order{}
	if err := pool.QueryRow(ctx,
		"SELECT id, item, qty, status FROM orders WHERE id=$1", id).
		Scan(&o.ID, &o.Item, &o.Qty, &o.Status); err != nil {
		return nil, err
	}
	return o, nil
}

// dbGetOrder returns nil (no error) when the order does not exist.
func dbGetOrder(ctx context.Context, id string) (*Order, error) {
	o := &Order{}
	err := pool.QueryRow(ctx,
		"SELECT id, item, qty, status FROM orders WHERE id=$1", id).
		Scan(&o.ID, &o.Item, &o.Qty, &o.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return o, nil
}
