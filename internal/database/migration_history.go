package database

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/tern/v2/migrate"
)

// format versioned migration names as `001_name.sql`.
var versionedMigrationName = regexp.MustCompile(`^([0-9]{3})_[a-z][a-z0-9_]*\.sql$`)

type migrationChecksum struct {
	version  int32
	name     string
	checksum string
}

func readMigrationChecksums(files fs.FS) ([]migrationChecksum, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migration directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if !versionedMigrationName.MatchString(entry.Name()) {
			return nil, fmt.Errorf("malformed migration name %q", entry.Name())
		}
	}

	paths, err := migrate.FindMigrations(files)
	if err != nil {
		return nil, fmt.Errorf("find ordered migrations: %w", err)
	}

	checksums := make([]migrationChecksum, 0, len(paths))
	for _, name := range paths {
		matches := versionedMigrationName.FindStringSubmatch(name)
		version, err := strconv.ParseInt(matches[1], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("parse migration version from %q: %w", name, err)
		}
		contents, err := fs.ReadFile(files, path.Clean(name))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		checksum := sha256.Sum256(contents)
		checksums = append(checksums, migrationChecksum{
			version:  int32(version),
			name:     name,
			checksum: fmt.Sprintf("%x", checksum),
		})
	}
	return checksums, nil
}

func attachHistoryRecords(migrations []*migrate.Migration, checksums []migrationChecksum) error {
	if len(migrations) != len(checksums) {
		return fmt.Errorf("Tern loaded %d migrations, but %d checksum records were prepared", len(migrations), len(checksums))
	}
	for index, item := range migrations {
		checksum := checksums[index]
		if item.Sequence != checksum.version || item.Name != checksum.name {
			return fmt.Errorf("Tern migration %d (%s) does not match embedded migration %d (%s)", item.Sequence, item.Name, checksum.version, checksum.name)
		}
		item.UpSQL += fmt.Sprintf(
			"\nINSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (%d, '%s', '%s', now());\n",
			checksum.version,
			strings.ReplaceAll(checksum.name, "'", "''"),
			checksum.checksum,
		)
	}
	return nil
}

func validateMigrationHistory(ctx context.Context, connection *pgxpool.Conn, checksums []migrationChecksum, currentVersion int32) error {
	if currentVersion < 0 || int(currentVersion) > len(checksums) {
		return fmt.Errorf("Tern schema version %d is outside embedded migration range 0..%d", currentVersion, len(checksums))
	}

	rows, err := connection.Query(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("read migration checksum history: %w", err)
	}
	defer rows.Close()

	byVersion := make(map[int32]migrationChecksum, len(checksums))
	for _, item := range checksums {
		byVersion[item.version] = item
	}
	seen := make(map[int32]bool, currentVersion)
	for rows.Next() {
		var version int32
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return fmt.Errorf("scan migration checksum history: %w", err)
		}
		item, exists := byVersion[version]
		if !exists {
			return fmt.Errorf("applied migration version %d (%s) is missing from the binary", version, name)
		}
		if item.name != name || item.checksum != checksum {
			return fmt.Errorf("applied migration %d (%s) does not match its embedded name and checksum", version, name)
		}
		if version > currentVersion {
			return fmt.Errorf("migration checksum history contains unapplied version %d", version)
		}
		seen[version] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read migration checksum history: %w", err)
	}
	for version := int32(1); version <= currentVersion; version++ {
		if !seen[version] {
			return fmt.Errorf("applied migration version %d is missing from checksum history", version)
		}
	}
	return nil
}
