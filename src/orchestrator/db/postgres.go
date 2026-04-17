package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lib/pq"
)

func OpenAndMigrate(databaseURL, migrationsDir string) (*sql.DB, error) {
	trimmedURL := strings.TrimSpace(databaseURL)
	if trimmedURL == "" {
		return nil, nil
	}

	if err := ensureDatabaseExists(trimmedURL); err != nil {
		return nil, err
	}

	db, err := sql.Open("postgres", trimmedURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres ping failed: %w", err)
	}

	if err := Migrate(db, migrationsDir); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func ensureDatabaseExists(databaseURL string) error {
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil
	}

	dbName := strings.TrimPrefix(parsed.Path, "/")
	dbName = strings.TrimSpace(dbName)
	if dbName == "" {
		return nil
	}

	adminURL := *parsed
	adminURL.Path = "/postgres"

	adminDB, err := sql.Open("postgres", adminURL.String())
	if err != nil {
		return fmt.Errorf("open postgres admin db: %w", err)
	}
	defer adminDB.Close()

	if err := adminDB.Ping(); err != nil {
		return fmt.Errorf("ping postgres admin db: %w", err)
	}

	var exists bool
	if err := adminDB.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists); err != nil {
		return fmt.Errorf("check database %s: %w", dbName, err)
	}
	if exists {
		return nil
	}

	if _, err := adminDB.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier(dbName)); err != nil {
		return fmt.Errorf("create database %s: %w", dbName, err)
	}
	return nil
}

func Migrate(db *sql.DB, migrationsDir string) error {
	if db == nil {
		return nil
	}
	dir := strings.TrimSpace(migrationsDir)
	if dir == "" {
		return nil
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		applied, err := migrationApplied(db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES ($1, NOW())`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

func migrationApplied(db *sql.DB, name string) (bool, error) {
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&exists); err != nil {
		return false, fmt.Errorf("check migration %s: %w", name, err)
	}
	return exists, nil
}
