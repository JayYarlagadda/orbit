package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/JayYarlagadda/orbit/internal/storage/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(); err != nil {
		logger.Error("migration failed", "error", err)
		os.Exit(1)
	}
	logger.Info("migration complete")
}

func run() error {
	directionFlag := flag.String("direction", "up", "migration direction: up or down")
	steps := flag.Int("steps", 0, "number of migrations; up defaults to all, down requires a positive value")
	directory := flag.String("directory", "migrations", "migration directory")
	databaseURL := flag.String("database-url", "", "PostgreSQL connection URL; defaults to ORBIT_DATABASE_URL")
	flag.Parse()

	if *databaseURL == "" {
		*databaseURL = os.Getenv("ORBIT_DATABASE_URL")
	}
	if *databaseURL == "" {
		return fmt.Errorf("database URL is required through -database-url or ORBIT_DATABASE_URL")
	}
	direction := migrate.Direction(*directionFlag)

	pool, err := pgxpool.New(context.Background(), *databaseURL)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return migrate.Apply(context.Background(), pool, *directory, direction, *steps)
}
