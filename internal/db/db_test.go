package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	handle, err := Open(filepath.Join(t.TempDir(), "db.sqlite3"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if _, err := handle.Exec(`CREATE TABLE probe (id integer NOT NULL PRIMARY KEY, note text)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return handle
}

// A canceled caller context used to leave the transaction open on the pooled
// connection: the ROLLBACK itself was issued with that dead context, so every
// later write ran inside the abandoned transaction (or failed to even begin).
func TestWithImmediateRollsBackAfterContextCancel(t *testing.T) {
	handle := openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())

	err := handle.WithImmediate(ctx, func(tx *Tx) error {
		if _, err := tx.Exec(`INSERT INTO probe (note) VALUES (?)`, "first"); err != nil {
			return err
		}
		cancel()
		return errors.New("caller aborted")
	})
	if err == nil {
		t.Fatal("want the callback error, got nil")
	}

	if err := handle.WithImmediate(context.Background(), func(tx *Tx) error {
		_, err := tx.Exec(`INSERT INTO probe (note) VALUES (?)`, "second")
		return err
	}); err != nil {
		t.Fatalf("second transaction failed: %v", err)
	}

	var count int
	if err := handle.QueryRow(`SELECT COUNT(1) FROM probe`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("rolled-back row leaked: count = %d", count)
	}
}

// A panic inside the callback must not hand a poisoned connection back.
func TestWithImmediateRollsBackAfterPanic(t *testing.T) {
	handle := openTestDB(t)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("want the panic to propagate")
			}
		}()
		_ = handle.WithImmediate(context.Background(), func(tx *Tx) error {
			if _, err := tx.Exec(`INSERT INTO probe (note) VALUES (?)`, "panic"); err != nil {
				return err
			}
			panic("boom")
		})
	}()

	if err := handle.WithImmediate(context.Background(), func(tx *Tx) error {
		_, err := tx.Exec(`INSERT INTO probe (note) VALUES (?)`, "after")
		return err
	}); err != nil {
		t.Fatalf("transaction after panic failed: %v", err)
	}

	var count int
	if err := handle.QueryRow(`SELECT COUNT(1) FROM probe`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("panicked row leaked: count = %d", count)
	}
}
