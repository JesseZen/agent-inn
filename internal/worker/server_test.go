package worker

import (
	"os"
	"testing"
	"time"
)

func TestWatchOrphanShutsDownOnStdinEOF(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	done := make(chan struct{})
	watchOrphan(reader, func() {
		close(done)
	})

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("orphan watcher did not run shutdown on EOF")
	}
}
