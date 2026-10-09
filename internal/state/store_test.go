package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flexdinesh/portforward/internal/forward"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	store, err := New(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testRecord(t *testing.T, port int) forward.Record {
	t.Helper()
	m, err := forward.NewMapping(forward.DefaultBind, port, "prod", "")
	if err != nil {
		t.Fatal(err)
	}
	return forward.Record{Mapping: m, ConnectionID: strings.Repeat("a", 32), Phase: forward.Active}
}

func TestRoundTripAndPrivatePermissions(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if rows, err := store.Read(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("empty read: %v, %v", rows, err)
	}
	if _, err := os.Stat(store.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty list must not create state directory")
	}
	want := testRecord(t, 5432)
	if err := store.WithLock(ctx, func(tx forward.Transaction) error { return tx.Save([]forward.Record{want}) }); err != nil {
		t.Fatal(err)
	}
	reopened, _ := New(store.root)
	got, err := reopened.Read(ctx)
	if err != nil || len(got) != 1 || got[0] != want {
		t.Fatalf("reopen: %v, %v", got, err)
	}
	for _, test := range []struct {
		name string
		mode os.FileMode
	}{{"", 0o700}, {"state.json", 0o600}, {"lock", 0o600}} {
		info, err := os.Stat(filepath.Join(store.root, test.name))
		if err != nil || info.Mode().Perm() != test.mode {
			t.Fatalf("unexpected permissions for %s: %v, %v", test.name, info, err)
		}
	}
}

func TestInvalidStatePreserved(t *testing.T) {
	record := testRecord(t, 5432)
	valid, _ := json.Marshal(document{Version: 1, Forwards: []forward.Record{record}})
	duplicate, _ := json.Marshal(document{Version: 1, Forwards: []forward.Record{record, record}})
	for _, text := range []string{
		`{`, `{ "version": 2, "forwards": [] }`, `{ "version": 1 }`,
		`{ "version": 1, "forwards": null }`, `{"version":1,"forwards":[],"unexpected":1}`,
		string(valid) + `{}`, string(duplicate),
		strings.Replace(string(valid), `"connection_id":"`+record.ConnectionID+`"`, `"connection_id":"../escape"`, 1),
		strings.Replace(string(valid), `"phase":"active"`, `"phase":"invalid"`, 1),
		strings.Replace(string(valid), `"local_port":5432`, `"local_port":0`, 1),
	} {
		t.Run(text, func(t *testing.T) {
			store := testStore(t)
			if err := os.Mkdir(store.root, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.root, "state.json")
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Read(context.Background()); err == nil {
				t.Fatal("invalid state accepted")
			}
			if err := store.WithLock(context.Background(), func(tx forward.Transaction) error { _, err := tx.Load(); return err }); err == nil {
				t.Fatal("mutation accepted invalid state")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != text {
				t.Fatal("invalid state must be preserved")
			}
		})
	}
}

func TestConcurrentWriters(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	var workers sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		record := testRecord(t, 5000+i)
		workers.Go(func() {
			// Separate handles exercise the same OS lock, not an in-memory mutex.
			other, _ := New(store.root)
			errors <- other.WithLock(ctx, func(tx forward.Transaction) error {
				rows, err := tx.Load()
				if err != nil {
					return err
				}
				return tx.Save(append(rows, record))
			})
		})
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.Read(ctx)
	if err != nil || len(rows) != 12 {
		t.Fatalf("lost updates: %d rows, %v", len(rows), err)
	}
}

func TestLockCancellation(t *testing.T) {
	store := testStore(t)
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- store.WithLock(context.Background(), func(tx forward.Transaction) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := store.WithLock(ctx, func(tx forward.Transaction) error { t.Error("contending operation admitted"); return nil })
	close(release)
	if holderErr := <-finished; holderErr != nil {
		t.Fatal(holderErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestRejectSymlinksAndPublicFiles(t *testing.T) {
	for _, name := range []string{"state.json", "lock"} {
		t.Run(name, func(t *testing.T) {
			store := testStore(t)
			if err := os.Mkdir(store.root, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.root, name)
			if err := os.Symlink(filepath.Join(t.TempDir(), "other"), path); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Read(context.Background()); err == nil {
				t.Fatal("symlink accepted")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`{"version":1,"forwards":[]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Read(context.Background()); err == nil {
				t.Fatal("public file accepted")
			}
		})
	}
}
