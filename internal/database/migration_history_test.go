package database

import (
	"testing"
	"testing/fstest"
)

func TestReadMigrationChecksumsUsesExactEmbeddedBytes(t *testing.T) {
	files := fstest.MapFS{
		"001_example.sql": &fstest.MapFile{Data: []byte("CREATE TABLE example (id integer);\n")},
	}

	got, err := readMigrationChecksums(files)
	if err != nil {
		t.Fatalf("read migration checksums: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("migration count = %d, want 1", len(got))
	}
	if got[0].version != 1 || got[0].name != "001_example.sql" || got[0].checksum != "9a84f800837cef2f6c386c8f5ea2ebb56dfd87555b4d28bd4c88ee98aff5f21a" {
		t.Errorf("migration metadata = %#v, want version 1, exact name and SHA-256 of embedded bytes", got[0])
	}
}

func TestReadMigrationChecksumsRejectsMalformedSQLName(t *testing.T) {
	files := fstest.MapFS{
		"bad-name.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}

	if _, err := readMigrationChecksums(files); err == nil {
		t.Fatal("readMigrationChecksums() error = nil, want malformed migration name error")
	}
}
