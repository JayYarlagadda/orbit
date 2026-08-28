package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Direction string

const (
	Up   Direction = "up"
	Down Direction = "down"
)

var migrationNamePattern = regexp.MustCompile(`^(\d{6})_([a-z0-9_]+)\.(up|down)\.sql$`)

type migration struct {
	version   int64
	name      string
	direction Direction
	path      string
}

func Apply(ctx context.Context, pool *pgxpool.Pool, directory string, direction Direction, steps int) error {
	if direction != Up && direction != Down {
		return fmt.Errorf("unsupported migration direction %q", direction)
	}
	if steps < 0 || (direction == Down && steps == 0) {
		return fmt.Errorf("down migrations require a positive step count")
	}

	migrations, err := discover(directory)
	if err != nil {
		return err
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.orbit_schema_migrations (
			version bigint PRIMARY KEY,
			name text NOT NULL,
			applied_at timestamptz NOT NULL
		)`); err != nil {
		return fmt.Errorf("ensure migration ledger: %w", err)
	}
	if _, err := connection.Exec(ctx,
		`SELECT pg_advisory_lock(hashtextextended('orbit-schema-migrations', 0))`,
	); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = connection.Exec(unlockContext,
			`SELECT pg_advisory_unlock(hashtextextended('orbit-schema-migrations', 0))`,
		)
	}()

	applied, err := loadApplied(ctx, connection)
	if err != nil {
		return err
	}
	if direction == Up {
		return applyUp(ctx, connection, migrations, applied, steps)
	}
	return applyDown(ctx, connection, migrations, applied, steps)
}

func discover(directory string) ([]migration, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read migration directory: %w", err)
	}

	var migrations []migration
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := migrationNamePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version in %s: %w", entry.Name(), err)
		}
		key := matches[1] + ":" + matches[3]
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate migration version and direction: %s", entry.Name())
		}
		seen[key] = struct{}{}
		migrations = append(migrations, migration{
			version:   version,
			name:      matches[2],
			direction: Direction(matches[3]),
			path:      filepath.Join(directory, entry.Name()),
		})
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no migrations found in %s", directory)
	}
	sort.Slice(migrations, func(i, j int) bool {
		if migrations[i].version != migrations[j].version {
			return migrations[i].version < migrations[j].version
		}
		return migrations[i].direction < migrations[j].direction
	})
	return migrations, nil
}

func loadApplied(ctx context.Context, connection *pgxpool.Conn) (map[int64]string, error) {
	rows, err := connection.Query(ctx,
		`SELECT version, name FROM public.orbit_schema_migrations ORDER BY version`,
	)
	if err != nil {
		return nil, fmt.Errorf("load applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[int64]string)
	for rows.Next() {
		var version int64
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}

func applyUp(
	ctx context.Context,
	connection *pgxpool.Conn,
	migrations []migration,
	applied map[int64]string,
	steps int,
) error {
	appliedCount := 0
	for _, candidate := range migrations {
		if candidate.direction != Up {
			continue
		}
		if appliedName, exists := applied[candidate.version]; exists {
			if appliedName != candidate.name {
				return fmt.Errorf("migration %06d name changed from %q to %q", candidate.version, appliedName, candidate.name)
			}
			continue
		}
		if steps > 0 && appliedCount >= steps {
			break
		}
		if err := runMigration(ctx, connection, candidate, true); err != nil {
			return err
		}
		appliedCount++
	}
	return nil
}

func applyDown(
	ctx context.Context,
	connection *pgxpool.Conn,
	migrations []migration,
	applied map[int64]string,
	steps int,
) error {
	downByVersion := make(map[int64]migration)
	var versions []int64
	for _, candidate := range migrations {
		if candidate.direction == Down {
			downByVersion[candidate.version] = candidate
		}
	}
	for version := range applied {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
	if len(versions) < steps {
		return fmt.Errorf("cannot roll back %d migrations; only %d are applied", steps, len(versions))
	}
	for _, version := range versions[:steps] {
		candidate, exists := downByVersion[version]
		if !exists {
			return fmt.Errorf("migration %06d has no down file", version)
		}
		if candidate.name != applied[version] {
			return fmt.Errorf("migration %06d down name %q does not match applied name %q", version, candidate.name, applied[version])
		}
		if err := runMigration(ctx, connection, candidate, false); err != nil {
			return err
		}
	}
	return nil
}

func runMigration(ctx context.Context, connection *pgxpool.Conn, candidate migration, applying bool) error {
	contents, err := os.ReadFile(candidate.path)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", candidate.path, err)
	}
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migration %06d: %w", candidate.version, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, string(contents)); err != nil {
		return fmt.Errorf("execute migration %06d %s: %w", candidate.version, candidate.direction, err)
	}
	if applying {
		_, err = tx.Exec(ctx, `
			INSERT INTO public.orbit_schema_migrations (version, name, applied_at)
			VALUES ($1, $2, clock_timestamp())`,
			candidate.version,
			candidate.name,
		)
	} else {
		_, err = tx.Exec(ctx,
			`DELETE FROM public.orbit_schema_migrations WHERE version = $1`,
			candidate.version,
		)
	}
	if err != nil {
		return fmt.Errorf("update migration ledger for %06d: %w", candidate.version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %06d: %w", candidate.version, err)
	}
	return nil
}
