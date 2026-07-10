package worker

import (
	"errors"
	"io"
	"sync"
	"time"
)

var (
	ErrStreamFirstByteTimeout = errors.New("upstream stream first byte timeout")
	ErrStreamIdleTimeout      = errors.New("upstream stream idle timeout")
)

type streamDeadlineReadCloser struct {
	mu         sync.Mutex
	source     io.ReadCloser
	idle       time.Duration
	timer      *time.Timer
	timeoutErr error
	closed     bool
}

func newStreamDeadlineReadCloser(source io.ReadCloser, firstByte time.Duration, idle time.Duration) *streamDeadlineReadCloser {
	reader := &streamDeadlineReadCloser{source: source, idle: idle}
	reader.arm(firstByte, ErrStreamFirstByteTimeout)
	return reader
}

func (r *streamDeadlineReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.source.Read(buffer)

	r.mu.Lock()
	if r.timeoutErr != nil {
		timeoutErr := r.timeoutErr
		r.mu.Unlock()
		return 0, timeoutErr
	}
	if n > 0 {
		if err == nil {
			r.armLocked(r.idle, ErrStreamIdleTimeout)
		} else {
			r.stopTimerLocked()
		}
	} else if err != nil {
		r.stopTimerLocked()
	}
	r.mu.Unlock()
	return n, err
}

func (r *streamDeadlineReadCloser) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.stopTimerLocked()
	r.mu.Unlock()
	return r.source.Close()
}

func (r *streamDeadlineReadCloser) arm(duration time.Duration, timeoutErr error) {
	r.mu.Lock()
	r.armLocked(duration, timeoutErr)
	r.mu.Unlock()
}

func (r *streamDeadlineReadCloser) armLocked(duration time.Duration, timeoutErr error) {
	r.stopTimerLocked()
	if duration <= 0 || r.closed {
		return
	}
	r.timer = time.AfterFunc(duration, func() {
		r.mu.Lock()
		if r.closed || r.timeoutErr != nil {
			r.mu.Unlock()
			return
		}
		r.timeoutErr = timeoutErr
		r.mu.Unlock()
		_ = r.source.Close()
	})
}

func (r *streamDeadlineReadCloser) stopTimerLocked() {
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
}
