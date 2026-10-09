package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/flexdinesh/portforward/internal/forward"
	"github.com/flexdinesh/portforward/internal/privatefs"
)

type Store struct {
	root string
}

type transaction struct {
	root string
}

type document struct {
	Version  int              `json:"version"`
	Forwards []forward.Record `json:"forwards"`
}

func New(root string) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("state directory must be absolute")
	}
	return &Store{root: filepath.Clean(root)}, nil
}

func (s *Store) Read(ctx context.Context) ([]forward.Record, error) {
	if err := privatefs.Dir(s.root, false); errors.Is(err, os.ErrNotExist) {
		return []forward.Record{}, nil
	} else if err != nil {
		return nil, err
	}
	var records []forward.Record
	err := s.lock(ctx, false, func(tx forward.Transaction) error {
		var err error
		records, err = tx.Load()
		return err
	})
	return records, err
}

func (s *Store) WithLock(ctx context.Context, operation func(forward.Transaction) error) error {
	if err := privatefs.Dir(s.root, true); err != nil {
		return err
	}
	return s.lock(ctx, true, operation)
}

func (s *Store) lock(ctx context.Context, exclusive bool, operation func(forward.Transaction) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	file, err := privatefs.Open(filepath.Join(s.root, "lock"), os.O_CREATE|os.O_RDWR)
	if err != nil {
		return err
	}
	defer file.Close()
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for state lock: %w", err)
		}
		err = syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return operation(&transaction{root: s.root})
}

func (s *transaction) Load() ([]forward.Record, error) {
	file, err := privatefs.Open(filepath.Join(s.root, "state.json"), os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return []forward.Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 8<<20 {
		return nil, fmt.Errorf("state exceeds 8 MiB")
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var data document
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("decode state: %w", err)
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, fmt.Errorf("state must contain one JSON document")
	}
	if data.Version != 1 || data.Forwards == nil {
		return nil, fmt.Errorf("unsupported or incomplete state schema (version %d)", data.Version)
	}
	if err := validate(data.Forwards); err != nil {
		return nil, fmt.Errorf("invalid state: %w", err)
	}
	return data.Forwards, nil
}

func (s *transaction) Save(records []forward.Record) error {
	if err := validate(records); err != nil {
		return err
	}
	if records == nil {
		records = []forward.Record{}
	}
	data, err := json.MarshalIndent(document{Version: 1, Forwards: records}, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > 8<<20 {
		return fmt.Errorf("state exceeds 8 MiB")
	}
	file, err := os.CreateTemp(s.root, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(s.root, "state.json")); err != nil {
		return err
	}
	dir, err := os.Open(s.root)
	if err != nil {
		return &forward.PublishedError{Err: err}
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return &forward.PublishedError{Err: err}
	}
	return nil
}

func validate(records []forward.Record) error {
	listeners := make(map[string]bool)
	hosts := make(map[string]string)
	for _, record := range records {
		if err := record.Validate(); err != nil {
			return err
		}
		if listeners[record.Local()] {
			return fmt.Errorf("duplicate listener %s", record.Local())
		}
		listeners[record.Local()] = true
		if host, ok := hosts[record.ConnectionID]; ok && host != record.SSHHost {
			return fmt.Errorf("connection ID assigned to different SSH hosts")
		}
		hosts[record.ConnectionID] = record.SSHHost
	}
	return nil
}
