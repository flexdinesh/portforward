package forward

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Transaction is held under the store's cross-process lock. Save atomically
// replaces the snapshot; callers record intent before external side effects.
// A PublishedError means the replacement is visible but durability is uncertain.
type Transaction interface {
	Load() ([]Record, error)
	Save([]Record) error
}

type PublishedError struct {
	Err error
}

func (e *PublishedError) Error() string {
	return fmt.Sprintf("state published but durability confirmation failed: %v", e.Err)
}

func (e *PublishedError) Unwrap() error { return e.Err }

type Store interface {
	Read(context.Context) ([]Record, error)
	WithLock(context.Context, func(Transaction) error) error
}

// Tunnels owns private SSH masters. Forward and Cancel must be idempotent for
// an identical mapping. Control operations must not authenticate or reconnect.
type Tunnels interface {
	Alive(context.Context, Connection) (bool, error)
	Start(context.Context, Connection) error
	Forward(context.Context, Connection, Mapping) error
	Cancel(context.Context, Connection, Mapping) error
	Stop(context.Context, Connection) error
}

type Manager struct {
	store   Store
	tunnels Tunnels
}

func New(store Store, tunnels Tunnels) *Manager {
	return &Manager{store: store, tunnels: tunnels}
}

type Entry struct {
	Mapping
	Status string `json:"status"`
}

func (m *Manager) List(ctx context.Context) ([]Entry, []error, error) {
	records, err := m.store.Read(ctx)
	if err != nil {
		return nil, nil, err
	}
	type health struct {
		alive bool
		err   error
	}
	connections := make(map[Connection]health)
	entries := make([]Entry, 0, len(records))
	var diagnostics []error
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		connection := record.Connection()
		h, ok := connections[connection]
		if !ok {
			h.alive, h.err = m.tunnels.Alive(ctx, connection)
			connections[connection] = h
			if h.err != nil {
				diagnostics = append(diagnostics, fmt.Errorf("%s: %w", connection.Host, h.err))
			}
		}
		status := "disconnected"
		if h.err != nil || h.alive && record.Phase != Active {
			status = "unknown"
			if h.err == nil {
				diagnostics = append(diagnostics, fmt.Errorf("%s: pending %s; retry add or remove", record.Local(), record.Phase))
			}
		} else if h.alive {
			status = "active"
		}
		entries = append(entries, Entry{Mapping: record.Mapping, Status: status})
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		if a.BindAddress < b.BindAddress {
			return -1
		}
		if a.BindAddress > b.BindAddress {
			return 1
		}
		return a.LocalPort - b.LocalPort
	})
	return entries, diagnostics, nil
}

func (m *Manager) Add(ctx context.Context, mapping Mapping) (bool, error) {
	if err := mapping.Validate(); err != nil {
		return false, err
	}
	changed := false
	err := m.store.WithLock(ctx, func(tx Transaction) error {
		var err error
		changed, err = m.add(ctx, tx, mapping)
		return err
	})
	return changed, err
}

// Reconnect restores disconnected mappings while holding the state lock so a
// concurrent remove cannot be undone. Pending removals are never restored.
func (m *Manager) Reconnect(ctx context.Context) ([]Mapping, error) {
	var restored []Mapping
	err := m.store.WithLock(ctx, func(tx Transaction) error {
		records, err := tx.Load()
		if err != nil {
			return err
		}
		slices.SortFunc(records, func(a, b Record) int {
			if a.BindAddress < b.BindAddress {
				return -1
			}
			if a.BindAddress > b.BindAddress {
				return 1
			}
			return a.LocalPort - b.LocalPort
		})
		type health struct {
			alive bool
			err   error
		}
		connections := make(map[Connection]health)
		var failures []error
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				return errors.Join(append(failures, err)...)
			}
			if record.Phase == Removing {
				continue
			}
			connection := record.Connection()
			h, ok := connections[connection]
			if !ok {
				h.alive, h.err = m.tunnels.Alive(ctx, connection)
				connections[connection] = h
			}
			if h.err != nil {
				failures = append(failures, fmt.Errorf("%s through %s: %w", record.Local(), record.SSHHost, h.err))
				continue
			}
			if h.alive {
				if record.Phase != Active {
					failures = append(failures, fmt.Errorf("%s: pending %s; retry add or remove", record.Local(), record.Phase))
				}
				continue
			}
			changed, err := m.add(ctx, tx, record.Mapping)
			if changed {
				restored = append(restored, record.Mapping)
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("%s through %s: %w", record.Local(), record.SSHHost, err))
			}
		}
		return errors.Join(failures...)
	})
	return restored, err
}

func (m *Manager) add(ctx context.Context, tx Transaction, mapping Mapping) (bool, error) {
	records, err := tx.Load()
	if err != nil {
		return false, err
	}
	index := slices.IndexFunc(records, func(r Record) bool { return r.SameListener(mapping) })
	if index >= 0 {
		existing := records[index]
		if existing.Mapping != mapping {
			return false, fmt.Errorf("%s already maps to %s through %s; remove it first", mapping.Local(), existing.Destination(), existing.SSHHost)
		}
		if existing.Phase == Removing {
			return false, fmt.Errorf("%s has a pending removal; retry remove first", mapping.Local())
		}
		alive, err := m.tunnels.Alive(ctx, existing.Connection())
		if err != nil {
			return false, err
		}
		if alive && existing.Phase == Active {
			return false, nil
		}
	}
	connection, alive, err := m.connection(ctx, records, mapping.SSHHost)
	if err != nil {
		return false, err
	}
	original := slices.Clone(records)
	pending := Record{Mapping: mapping, ConnectionID: connection.ID, Phase: Adding}
	if index < 0 {
		index = len(records)
		records = append(records, pending)
	} else {
		records[index] = pending
	}
	if err := tx.Save(records); err != nil {
		return false, err
	}
	if !alive {
		if err := m.tunnels.Start(ctx, connection); err != nil {
			// Keep intent: authentication may have completed while the caller
			// was interrupted. A retry can find and clean up the owned master.
			cleanupCtx, cancel := cleanupContext(ctx)
			defer cancel()
			return false, errors.Join(err, m.tunnels.Stop(cleanupCtx, connection))
		}
	}
	if err := m.tunnels.Forward(ctx, connection, mapping); err != nil {
		return false, m.rollback(ctx, tx, original, connection, mapping, err)
	}
	records[index].Phase = Active
	if err := tx.Save(records); err != nil {
		var published *PublishedError
		if errors.As(err, &published) {
			// The visible snapshot already agrees with the live listener.
			// Rolling back SSH could contradict that published active state.
			return true, err
		}
		return false, m.rollback(ctx, tx, original, connection, mapping, err)
	}
	return true, nil
}

func (m *Manager) connection(ctx context.Context, records []Record, host string) (Connection, bool, error) {
	seen := make(map[Connection]bool)
	for _, record := range records {
		connection := record.Connection()
		if record.SSHHost != host || seen[connection] {
			continue
		}
		seen[connection] = true
		alive, err := m.tunnels.Alive(ctx, connection)
		if err != nil {
			return Connection{}, false, err
		}
		if alive {
			return connection, true, nil
		}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Connection{}, false, err
	}
	return Connection{Host: host, ID: hex.EncodeToString(id[:])}, false, nil
}

func (m *Manager) rollback(ctx context.Context, tx Transaction, original []Record, connection Connection, mapping Mapping, cause error) error {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()
	err := m.tunnels.Cancel(cleanupCtx, connection, mapping)
	if err == nil && !usesConnection(original, connection, -1) {
		err = m.tunnels.Stop(cleanupCtx, connection)
	}
	if err != nil {
		return errors.Join(cause, fmt.Errorf("rollback incomplete; retry remove: %w", err))
	}
	return errors.Join(cause, tx.Save(original))
}

func (m *Manager) Remove(ctx context.Context, bind string, port int, host string) (bool, error) {
	bind, err := NormalizeBind(bind)
	if err != nil {
		return false, err
	}
	if port < 1 || port > 65535 {
		return false, fmt.Errorf("ports must be between 1 and 65535")
	}
	if host != "" {
		if err := ValidateSSHHost(host); err != nil {
			return false, err
		}
	}
	removed := false
	err = m.store.WithLock(ctx, func(tx Transaction) error {
		records, err := tx.Load()
		if err != nil {
			return err
		}
		index := slices.IndexFunc(records, func(r Record) bool { return r.BindAddress == bind && r.LocalPort == port })
		if index < 0 {
			return nil
		}
		record := records[index]
		if host != "" && host != record.SSHHost {
			return fmt.Errorf("%s belongs to %s, not %s", record.Local(), record.SSHHost, host)
		}
		records[index].Phase = Removing
		if err := tx.Save(records); err != nil {
			return err
		}
		connection := record.Connection()
		if err := m.tunnels.Cancel(ctx, connection, record.Mapping); err != nil {
			return err
		}
		if !usesConnection(records, connection, index) {
			if err := m.tunnels.Stop(ctx, connection); err != nil {
				return err
			}
		}
		if err := tx.Save(slices.Delete(records, index, index+1)); err != nil {
			return err
		}
		removed = true
		return nil
	})
	return removed, err
}

func usesConnection(records []Record, connection Connection, except int) bool {
	for i, record := range records {
		if i != except && record.Connection() == connection {
			return true
		}
	}
	return false
}

func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}
