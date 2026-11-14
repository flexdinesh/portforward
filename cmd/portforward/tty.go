package main

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

const ttyWait = 50 * time.Millisecond

var errInputStopped = errors.New("terminal input stopped")

// ttyReader reads terminal input without leaving a read blocked after a
// control returns. A blocked read would steal later input on the same
// terminal, such as the port prompt or OpenSSH authentication. Waits use
// select(2), which, unlike poll(2), supports terminals on macOS.
type ttyReader struct {
	fd      int
	stopped atomic.Bool
	reading sync.Mutex
}

func (r *ttyReader) Read(buffer []byte) (int, error) {
	r.reading.Lock()
	defer r.reading.Unlock()
	for !r.stopped.Load() {
		var set unix.FdSet
		set.Set(r.fd)
		timeout := unix.NsecToTimeval(ttyWait.Nanoseconds())
		ready, err := unix.Select(r.fd+1, &set, nil, nil, &timeout)
		if errors.Is(err, unix.EINTR) || err == nil && ready == 0 {
			continue
		}
		if err != nil {
			return 0, err
		}
		// Input that arrives while stopping belongs to the next reader.
		if r.stopped.Load() {
			break
		}
		n, err := unix.Read(r.fd, buffer)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, io.EOF
		}
		return n, nil
	}
	return 0, errInputStopped
}

// stop ends reading and waits for any in-flight read to return.
func (r *ttyReader) stop() {
	r.stopped.Store(true)
	r.reading.Lock()
	defer r.reading.Unlock()
}
