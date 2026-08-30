package processtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayYarlagadda/orbit/internal/storage/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func requirePostgres(t *testing.T) string {
	t.Helper()
	databaseURL := os.Getenv("ORBIT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORBIT_TEST_DATABASE_URL is not set")
	}
	return databaseURL
}

func resetPostgres(t *testing.T, databaseURL string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	migrationDirectory, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, pool, migrationDirectory, migrate.Up, 0); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		TRUNCATE orbit.audit_events, orbit.delivery_attempts, orbit.commands,
		         orbit.device_cursors RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset database: %v", err)
	}
	return pool
}
