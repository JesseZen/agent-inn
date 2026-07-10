package worker

import (
	"io"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestStreamDeadlineReaderReportsFirstByteTimeout(t *testing.T) {
	body := newTimeoutTestBody()
	reader := newStreamDeadlineReadCloser(body, 20*time.Millisecond, 0)

	buffer := make([]byte, 16)
	n, err := reader.Read(buffer)

	got := streamTimeoutTestResult{N: n, Err: err, Closed: body.isClosed()}
	want := streamTimeoutTestResult{Err: ErrStreamFirstByteTimeout, Closed: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected first-byte timeout:\n got %#v\nwant %#v", got, want)
	}
}

func TestStreamDeadlineReaderReportsIdleTimeoutAfterData(t *testing.T) {
	body := newTimeoutTestBody()
	body.chunks <- []byte("data: ready\n\n")
	reader := newStreamDeadlineReadCloser(body, time.Second, 20*time.Millisecond)

	buffer := make([]byte, 32)
	firstN, firstErr := reader.Read(buffer)
	secondN, secondErr := reader.Read(buffer)

	got := struct {
		First  streamTimeoutTestResult
		Second streamTimeoutTestResult
	}{
		First:  streamTimeoutTestResult{N: firstN, Err: firstErr},
		Second: streamTimeoutTestResult{N: secondN, Err: secondErr, Closed: body.isClosed()},
	}
	want := struct {
		First  streamTimeoutTestResult
		Second streamTimeoutTestResult
	}{
		First:  streamTimeoutTestResult{N: len("data: ready\n\n")},
		Second: streamTimeoutTestResult{Err: ErrStreamIdleTimeout, Closed: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected idle timeout:\n got %#v\nwant %#v", got, want)
	}
}

type streamTimeoutTestResult struct {
	N      int
	Err    error
	Closed bool
}

type timeoutTestBody struct {
	chunks chan []byte
	closed chan struct{}
	once   sync.Once
}

func newTimeoutTestBody() *timeoutTestBody {
	return &timeoutTestBody{chunks: make(chan []byte, 1), closed: make(chan struct{})}
}

func (b *timeoutTestBody) Read(buffer []byte) (int, error) {
	select {
	case chunk := <-b.chunks:
		return copy(buffer, chunk), nil
	case <-b.closed:
		return 0, io.ErrClosedPipe
	}
}

func (b *timeoutTestBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func (b *timeoutTestBody) isClosed() bool {
	select {
	case <-b.closed:
		return true
	default:
		return false
	}
}
