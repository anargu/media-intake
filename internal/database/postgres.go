package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/tern/v2/migrate"
)

const (
	operationTimeout = 5 * time.Second
	advisoryLockKey  = int64(0x4d494752415445)
)

//go:embed schema_migrations.sql migrations
var migrationFiles embed.FS

// Open creates a PostgreSQL pool and verifies connectivity before returning it.
func Open(ctx context.Context, connectionString string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL pool configuration: %w", err)
	}

	openContext, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(openContext, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}

	pingContext, cancelPing := context.WithTimeout(ctx, operationTimeout)
	defer cancelPing()
	if err := pool.Ping(pingContext); err != nil {
		pool.Close()
		return nil, fmt.Errorf("verify PostgreSQL connection: %w", err)
	}
	return pool, nil
}

// Migrate applies all embedded migrations while holding a session advisory lock.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("migrate PostgreSQL schema: pool is nil")
	}

	migrationFS, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}
	checksums, err := readMigrationChecksums(migrationFS)
	if err != nil {
		return fmt.Errorf("read embedded migration checksums: %w", err)
	}

	acquireContext, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	connection, err := pool.Acquire(acquireContext)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer connection.Release()

	operationContext, cancelOperation := context.WithTimeout(ctx, operationTimeout)
	defer cancelOperation()
	if _, err := connection.Exec(operationContext, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		unlockContext, cancelUnlock := context.WithTimeout(context.Background(), operationTimeout)
		defer cancelUnlock()
		_, _ = connection.Exec(unlockContext, `SELECT pg_advisory_unlock($1)`, advisoryLockKey)
	}()

	bootstrapSQL, err := migrationFiles.ReadFile("schema_migrations.sql")
	if err != nil {
		return fmt.Errorf("read schema_migrations SQL: %w", err)
	}
	if _, err := connection.Exec(operationContext, string(bootstrapSQL)); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	migrator, err := migrate.NewMigrator(operationContext, connection.Conn(), "schema_version")
	if err != nil {
		return fmt.Errorf("initialize Tern migrator: %w", err)
	}
	if len(checksums) > 0 {
		if err := migrator.LoadMigrations(migrationFS); err != nil {
			return fmt.Errorf("load Tern migrations: %w", err)
		}
		if err := attachHistoryRecords(migrator.Migrations, checksums); err != nil {
			return err
		}
	}

	currentVersion, err := migrator.GetCurrentVersion(operationContext)
	if err != nil {
		return fmt.Errorf("read Tern migration version: %w", err)
	}
	if err := validateMigrationHistory(operationContext, connection, checksums, currentVersion); err != nil {
		return err
	}
	if len(checksums) > 0 {
		if err := migrator.Migrate(operationContext); err != nil {
			return fmt.Errorf("run Tern migrations: %w", err)
		}
		currentVersion, err = migrator.GetCurrentVersion(operationContext)
		if err != nil {
			return fmt.Errorf("read Tern migration version after apply: %w", err)
		}
		if err := validateMigrationHistory(operationContext, connection, checksums, currentVersion); err != nil {
			return err
		}
	}
	return nil
}
