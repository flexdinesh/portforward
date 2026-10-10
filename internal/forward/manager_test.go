package forward_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/portforward/internal/forward"
	"github.com/flexdinesh/portforward/internal/state"
)

type tunnels struct {
	live         map[forward.Connection]bool
	listeners    map[string]forward.Connection
	failStart    bool
	failCancel   bool
	failCheck    bool
	starts       int
	afterForward func()
}

func (s *tunnels) Alive(ctx context.Context, c forward.Connection) (bool, error) {
	if s.failCheck {
		return false, errors.New("control timeout")
	}
	return s.live[c], ctx.Err()
}

func (s *tunnels) Start(ctx context.Context, c forward.Connection) error {
	if s.failStart {
		return errors.New("authentication failed")
	}
	s.starts++
	s.live[c] = true
	return ctx.Err()
}

func (s *tunnels) Forward(ctx context.Context, c forward.Connection, m forward.Mapping) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !s.live[c] {
		return errors.New("master disconnected")
	}
	if existing, ok := s.listeners[m.Local()]; ok && existing != c {
		return errors.New("port busy")
	}
	s.listeners[m.Local()] = c
	if s.afterForward != nil {
		s.afterForward()
	}
	return ctx.Err()
}

func (s *tunnels) Cancel(ctx context.Context, c forward.Connection, m forward.Mapping) error {
	if s.failCancel {
		return errors.New("cancel failed")
	}
	if s.listeners[m.Local()] == c {
		delete(s.listeners, m.Local())
	}
	return ctx.Err()
}

func (s *tunnels) Stop(ctx context.Context, c forward.Connection) error {
	delete(s.live, c)
	for key, owner := range s.listeners {
		if owner == c {
			delete(s.listeners, key)
		}
	}
	return ctx.Err()
}

func setup(t *testing.T) (*state.Store, *tunnels, *forward.Manager) {
	t.Helper()
	store, err := state.New(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	backend := &tunnels{live: make(map[forward.Connection]bool), listeners: make(map[string]forward.Connection)}
	return store, backend, forward.New(store, backend)
}

func mapping(t *testing.T, port int, host string) forward.Mapping {
	t.Helper()
	m, err := forward.NewMapping(forward.DefaultBind, port, host, "")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func records(t *testing.T, store *state.Store) []forward.Record {
	t.Helper()
	got, err := store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLifecycle(t *testing.T) {
	ctx := context.Background()
	store, backend, manager := setup(t)
	a, b := mapping(t, 15432, "prod"), mapping(t, 5432, "prod")
	for _, m := range []forward.Mapping{a, b} {
		if changed, err := manager.Add(ctx, m); err != nil || !changed {
			t.Fatalf("add: changed=%v err=%v", changed, err)
		}
	}
	if backend.starts != 1 {
		t.Fatal("same host must share a master")
	}
	if changed, err := manager.Add(ctx, a); err != nil || changed {
		t.Fatalf("duplicate: changed=%v err=%v", changed, err)
	}
	if _, err := manager.Add(ctx, mapping(t, a.LocalPort, "other")); err == nil {
		t.Fatal("conflicting mapping must fail")
	}
	if _, err := manager.Remove(ctx, a.BindAddress, a.LocalPort, "other"); err == nil {
		t.Fatal("host guard must fail")
	}
	entries, diagnostics, err := manager.List(ctx)
	if err != nil || len(diagnostics) != 0 || len(entries) != 2 || entries[0].LocalPort != 5432 || entries[0].Status != "active" {
		t.Fatalf("list: %+v, %v, %v", entries, diagnostics, err)
	}
	if _, err := manager.Remove(ctx, a.BindAddress, a.LocalPort, ""); err != nil {
		t.Fatal(err)
	}
	if len(backend.live) != 1 || len(backend.listeners) != 1 {
		t.Fatal("removal must preserve sibling tunnel")
	}
	if changed, err := manager.Remove(ctx, a.BindAddress, a.LocalPort, ""); err != nil || changed {
		t.Fatal("missing removal must succeed")
	}
	if _, err := manager.Remove(ctx, b.BindAddress, b.LocalPort, "prod"); err != nil {
		t.Fatal(err)
	}
	if len(backend.live) != 0 || len(records(t, store)) != 0 {
		t.Fatal("last removal must close master and remove state")
	}
}

func TestReconnectDoesNotReviveDeadSiblings(t *testing.T) {
	ctx := context.Background()
	store, backend, manager := setup(t)
	a, b := mapping(t, 5432, "prod"), mapping(t, 15432, "prod")
	for _, m := range []forward.Mapping{a, b} {
		if _, err := manager.Add(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	old := records(t, store)[0].Connection()
	backend.Stop(ctx, old)
	if _, err := manager.Add(ctx, a); err != nil {
		t.Fatal(err)
	}
	entries, _, err := manager.List(ctx)
	if err != nil || entries[0].Status != "active" || entries[1].Status != "disconnected" {
		t.Fatalf("dead sibling must stay disconnected: %+v, %v", entries, err)
	}
	if _, err := manager.Remove(ctx, b.BindAddress, b.LocalPort, ""); err != nil {
		t.Fatal(err)
	}
	if len(backend.live) != 1 {
		t.Fatal("removing old generation must preserve replacement master")
	}
}

func TestReconnectRestoresSavedMappings(t *testing.T) {
	ctx := context.Background()
	store, backend, manager := setup(t)
	a, err := forward.NewMapping("::1", 15432, "prod", "db.internal:5432")
	if err != nil {
		t.Fatal(err)
	}
	b, active := mapping(t, 5432, "prod"), mapping(t, 8080, "other")
	for _, m := range []forward.Mapping{a, active, b} {
		if _, err := manager.Add(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	before := records(t, store)
	if err := backend.Stop(ctx, before[0].Connection()); err != nil {
		t.Fatal(err)
	}
	restored, err := manager.Reconnect(ctx)
	if err != nil || !slices.Equal(restored, []forward.Mapping{b, a}) {
		t.Fatalf("reconnect saved mappings in list order: %+v, %v", restored, err)
	}
	after := records(t, store)
	if after[1] != before[1] {
		t.Fatal("active forward must retain its generation and mapping")
	}
	if after[0].ConnectionID == before[0].ConnectionID || after[0].ConnectionID != after[2].ConnectionID || backend.starts != 3 {
		t.Fatal("restored siblings must share one new generation")
	}
	for _, m := range []forward.Mapping{a, b, active} {
		if _, ok := backend.listeners[m.Local()]; !ok {
			t.Fatalf("listener missing: %s", m.Local())
		}
	}
	entries, diagnostics, err := manager.List(ctx)
	if err != nil || len(diagnostics) != 0 || len(entries) != 3 {
		t.Fatalf("list: %+v, %v, %v", entries, diagnostics, err)
	}
	for _, entry := range entries {
		if entry.Status != "active" {
			t.Fatalf("forward not restored: %+v", entry)
		}
	}
	restored, err = manager.Reconnect(ctx)
	if err != nil || len(restored) != 0 || !slices.Equal(after, records(t, store)) || backend.starts != 3 {
		t.Fatalf("active reconnect must be a no-op: %+v, %v", restored, err)
	}
}

func TestReconnectContinuesAfterFailure(t *testing.T) {
	ctx := context.Background()
	store, backend, manager := setup(t)
	a, b := mapping(t, 5432, "prod"), mapping(t, 15432, "prod")
	for _, m := range []forward.Mapping{a, b} {
		if _, err := manager.Add(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	before := records(t, store)
	backend.Stop(ctx, before[0].Connection())
	unrelated := forward.Connection{Host: "unrelated", ID: strings.Repeat("a", 32)}
	backend.live[unrelated] = true
	backend.listeners[a.Local()] = unrelated
	restored, err := manager.Reconnect(ctx)
	if err == nil || !strings.Contains(err.Error(), a.Local()) || !strings.Contains(err.Error(), "port busy") || !slices.Equal(restored, []forward.Mapping{b}) {
		t.Fatalf("partial reconnect: %+v, %v", restored, err)
	}
	after := records(t, store)
	if after[0] != before[0] || after[1].ConnectionID == before[1].ConnectionID || after[1].Phase != forward.Active {
		t.Fatal("failure must preserve its old record and allow a sibling to reconnect")
	}
	if !backend.live[unrelated] || backend.listeners[a.Local()] != unrelated {
		t.Fatal("busy listener and unrelated master must remain untouched")
	}
	delete(backend.listeners, a.Local())
	restored, err = manager.Reconnect(ctx)
	if err != nil || !slices.Equal(restored, []forward.Mapping{a}) {
		t.Fatalf("retry failed mapping: %+v, %v", restored, err)
	}
	after = records(t, store)
	if after[0].ConnectionID != after[1].ConnectionID {
		t.Fatal("retry must reuse the restored sibling's master")
	}
}

func TestReconnectPendingPhases(t *testing.T) {
	for _, phase := range []forward.Phase{forward.Adding, forward.Removing} {
		for _, alive := range []bool{false, true} {
			t.Run(string(phase)+"/"+map[bool]string{false: "dead", true: "live"}[alive], func(t *testing.T) {
				ctx := context.Background()
				store, backend, manager := setup(t)
				a := mapping(t, 5432, "prod")
				if _, err := manager.Add(ctx, a); err != nil {
					t.Fatal(err)
				}
				if err := store.WithLock(ctx, func(tx forward.Transaction) error {
					rows, err := tx.Load()
					if err != nil {
						return err
					}
					rows[0].Phase = phase
					return tx.Save(rows)
				}); err != nil {
					t.Fatal(err)
				}
				before := records(t, store)
				if !alive {
					backend.Stop(ctx, before[0].Connection())
				}
				restored, err := manager.Reconnect(ctx)
				if phase == forward.Adding && !alive {
					if err != nil || !slices.Equal(restored, []forward.Mapping{a}) || records(t, store)[0].Phase != forward.Active {
						t.Fatalf("dead pending add must recover: %+v, %v", restored, err)
					}
					return
				}
				if len(restored) != 0 || !slices.Equal(before, records(t, store)) || backend.starts != 1 {
					t.Fatalf("pending operation must stay untouched: %+v, %v", restored, err)
				}
				if phase == forward.Adding && (err == nil || !strings.Contains(err.Error(), "pending adding")) {
					t.Fatalf("live pending add must report unknown status: %v", err)
				}
				if phase == forward.Removing && err != nil {
					t.Fatalf("pending removal should be skipped: %v", err)
				}
			})
		}
	}
}

func TestReconnectInspectionFailurePreservesState(t *testing.T) {
	ctx := context.Background()
	store, backend, manager := setup(t)
	if _, err := manager.Add(ctx, mapping(t, 5432, "prod")); err != nil {
		t.Fatal(err)
	}
	before := records(t, store)
	backend.failCheck = true
	restored, err := manager.Reconnect(ctx)
	if err == nil || !strings.Contains(err.Error(), "control timeout") || len(restored) != 0 || backend.starts != 1 || !slices.Equal(before, records(t, store)) {
		t.Fatalf("unknown status must not reconnect: %+v, %v", restored, err)
	}
}

func TestReconnectCancellationStopsRemainingAttempts(t *testing.T) {
	store, backend, manager := setup(t)
	a, b := mapping(t, 5432, "prod"), mapping(t, 15432, "other")
	for _, m := range []forward.Mapping{a, b} {
		if _, err := manager.Add(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	before := records(t, store)
	for _, record := range before {
		backend.Stop(context.Background(), record.Connection())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend.afterForward = cancel
	restored, err := manager.Reconnect(ctx)
	if !errors.Is(err, context.Canceled) || len(restored) != 0 || backend.starts != 3 || len(backend.live) != 0 || !slices.Equal(before, records(t, store)) {
		t.Fatalf("cancellation must compensate and stop before next host: %+v, %v", restored, err)
	}
}

func TestReconnectPersistenceFailures(t *testing.T) {
	for _, test := range []struct {
		name      string
		failAt    int
		published bool
	}{
		{"intent write", 1, false},
		{"intent durability", 1, true},
		{"active write", 2, false},
		{"active durability", 2, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, backend, manager := setup(t)
			a := mapping(t, 5432, "prod")
			if _, err := manager.Add(ctx, a); err != nil {
				t.Fatal(err)
			}
			before := records(t, store)
			backend.Stop(ctx, before[0].Connection())
			failures := &failingStore{Store: store, failAt: test.failAt, published: test.published}
			restored, err := forward.New(failures, backend).Reconnect(ctx)
			if err == nil {
				t.Fatal("persistence failure must be reported")
			}
			after := records(t, store)
			if test.published && test.failAt == 2 {
				if !slices.Equal(restored, []forward.Mapping{a}) || after[0].Phase != forward.Active || len(backend.listeners) != 1 {
					t.Fatalf("published active state must keep restored listener: %+v, %+v", restored, after)
				}
				return
			}
			if len(restored) != 0 || len(backend.listeners) != 0 || len(backend.live) != 0 {
				t.Fatal("failed reconnect must leave no new listener or master")
			}
			if test.published {
				if len(after) != 1 || after[0].Phase != forward.Adding || after[0].Mapping != a {
					t.Fatal("published intent must remain retryable")
				}
			} else if !slices.Equal(before, after) {
				t.Fatal("unpublished failure must preserve the original state")
			}
		})
	}
}

type failingStore struct {
	forward.Store
	saves     int
	failAt    int
	published bool
}

type failingTransaction struct {
	forward.Transaction
	store *failingStore
}

func (s *failingStore) WithLock(ctx context.Context, operation func(forward.Transaction) error) error {
	return s.Store.WithLock(ctx, func(tx forward.Transaction) error {
		return operation(&failingTransaction{Transaction: tx, store: s})
	})
}

func (tx *failingTransaction) Save(records []forward.Record) error {
	tx.store.saves++
	if tx.store.saves == tx.store.failAt {
		if tx.store.published {
			if err := tx.Transaction.Save(records); err != nil {
				return err
			}
			return &forward.PublishedError{Err: errors.New("directory sync failed")}
		}
		return errors.New("disk full")
	}
	return tx.Transaction.Save(records)
}

func TestPublishedAddFailureKeepsListenerConsistent(t *testing.T) {
	ctx := context.Background()
	store, backend, _ := setup(t)
	failures := &failingStore{Store: store, failAt: 2, published: true}
	manager := forward.New(failures, backend)
	a := mapping(t, 5432, "prod")
	if _, err := manager.Add(ctx, a); err == nil {
		t.Fatal("durability error must be returned")
	}
	rows := records(t, store)
	if len(rows) != 1 || rows[0].Phase != forward.Active || len(backend.listeners) != 1 {
		t.Fatal("published state must agree with the listener")
	}
	if changed, err := manager.Add(ctx, a); err != nil || changed {
		t.Fatalf("retry: %v, %v", changed, err)
	}
}

func TestAddPersistenceFailures(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			store, backend, _ := setup(t)
			failures := &failingStore{Store: store, failAt: failAt}
			manager := forward.New(failures, backend)
			if _, err := manager.Add(context.Background(), mapping(t, 5432, "prod")); err == nil {
				t.Fatal("disk failure must be returned")
			}
			if len(records(t, store)) != 0 || len(backend.live) != 0 || len(backend.listeners) != 0 {
				t.Fatal("failed add must roll back listener, master, and state")
			}
		})
	}
}

func TestRollbackPreservesExistingTunnel(t *testing.T) {
	ctx := context.Background()
	store, backend, manager := setup(t)
	a := mapping(t, 5432, "prod")
	if _, err := manager.Add(ctx, a); err != nil {
		t.Fatal(err)
	}
	before := records(t, store)
	failures := &failingStore{Store: store, failAt: 2}
	if _, err := forward.New(failures, backend).Add(ctx, mapping(t, 15432, "prod")); err == nil {
		t.Fatal("expected commit failure")
	}
	if !slices.Equal(before, records(t, store)) || len(backend.live) != 1 || len(backend.listeners) != 1 {
		t.Fatal("rollback must preserve existing sibling")
	}
}

func TestPendingOperationsRecover(t *testing.T) {
	ctx := context.Background()
	store, backend, _ := setup(t)
	failures := &failingStore{Store: store, failAt: 2}
	manager := forward.New(failures, backend)
	a := mapping(t, 5432, "prod")
	backend.failCancel = true
	if _, err := manager.Add(ctx, a); err == nil {
		t.Fatal("expected failed rollback")
	}
	if got := records(t, store); len(got) != 1 || got[0].Phase != forward.Adding {
		t.Fatal("failed rollback must leave recoverable intent")
	}
	entries, diagnostics, _ := manager.List(ctx)
	if entries[0].Status != "unknown" || len(diagnostics) == 0 {
		t.Fatal("pending add must not claim active")
	}
	backend.failCancel = false
	if changed, err := manager.Add(ctx, a); err != nil || !changed || backend.starts != 1 {
		t.Fatalf("retry pending add: %v, %v", changed, err)
	}
	// The final removal write fails after cancellation and master shutdown.
	failures.failAt = failures.saves + 2
	if _, err := manager.Remove(ctx, a.BindAddress, a.LocalPort, ""); err == nil {
		t.Fatal("expected failed removal commit")
	}
	if got := records(t, store); len(got) != 1 || got[0].Phase != forward.Removing {
		t.Fatal("failed removal must preserve intent")
	}
	if _, err := manager.Add(ctx, a); err == nil {
		t.Fatal("add must not bypass pending removal")
	}
	if removed, err := manager.Remove(ctx, a.BindAddress, a.LocalPort, ""); err != nil || !removed {
		t.Fatalf("retry remove: %v, %v", removed, err)
	}
}

func TestFailedAuthenticationRetainsIntent(t *testing.T) {
	ctx := context.Background()
	store, backend, manager := setup(t)
	backend.failStart = true
	a := mapping(t, 5432, "prod")
	if _, err := manager.Add(ctx, a); err == nil {
		t.Fatal("expected authentication failure")
	}
	if got := records(t, store); len(got) != 1 || got[0].Phase != forward.Adding {
		t.Fatal("failed start must retain intent")
	}
	backend.failStart = false
	if _, err := manager.Add(ctx, a); err != nil {
		t.Fatal(err)
	}
}

func TestListInspectionFailure(t *testing.T) {
	_, backend, manager := setup(t)
	ctx := context.Background()
	if _, err := manager.Add(ctx, mapping(t, 5432, "prod")); err != nil {
		t.Fatal(err)
	}
	backend.failCheck = true
	entries, diagnostics, err := manager.List(ctx)
	if err != nil || len(diagnostics) != 1 || entries[0].Status != "unknown" {
		t.Fatalf("inspection failure: %+v, %v, %v", entries, diagnostics, err)
	}
}

func TestCancellationRollsBackAcceptedForward(t *testing.T) {
	store, backend, manager := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend.afterForward = cancel
	if _, err := manager.Add(ctx, mapping(t, 5432, "prod")); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if len(backend.live) != 0 || len(backend.listeners) != 0 || len(records(t, store)) != 0 {
		t.Fatal("cancelled caller must still compensate accepted effects")
	}
}
