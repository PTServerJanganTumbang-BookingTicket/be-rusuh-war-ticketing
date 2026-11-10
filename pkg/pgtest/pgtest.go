// Package pgtest gives a test its own throwaway Postgres database with every migration applied.
//
// It is imported only from _test.go files. The test is skipped, not failed, when DATABASE_URL is unset or
// the run is -short, the same rule the Gherkin suites use for @integration. `make verify` sets DATABASE_URL.
package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// New creates database test_<nanos>, applies migrations/*.up.sql in order, and returns a pool on it.
// The database is dropped when the test ends.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	server := os.Getenv("DATABASE_URL")
	if server == "" || testing.Short() {
		t.Skip("pgtest: set DATABASE_URL and drop -short (make verify does) to run Postgres tests")
	}
	ctx := context.Background()
	srv, err := pgxpool.New(ctx, server)
	if err != nil {
		t.Fatalf("pgtest: connect: %v", err)
	}
	name := fmt.Sprintf("pgtest_%d", time.Now().UnixNano())
	if _, err := srv.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		srv.Close()
		t.Fatalf("pgtest: create database: %v", err)
	}
	u, err := url.Parse(server)
	if err != nil {
		t.Fatalf("pgtest: parse DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatalf("pgtest: open: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = srv.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		srv.Close()
	})
	files, err := filepath.Glob(filepath.Join(migrationsDir(t), "*.up.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("pgtest: no migrations found: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("pgtest: migration %s: %v", filepath.Base(f), err)
		}
	}
	return pool
}

// migrationsDir walks up from the test's folder to the repository's migrations/ folder.
func migrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, "migrations")); err == nil && fi.IsDir() {
			return filepath.Join(dir, "migrations")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("pgtest: migrations/ not found above the test folder")
		}
		dir = parent
	}
}
