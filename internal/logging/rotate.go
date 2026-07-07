package logging

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const (
	DefaultRotateMaxBytes = 10 * 1024 * 1024
	DefaultRotateKeep     = 3
)

// RotatingWriter appends to a file, rotating it aside once it exceeds maxBytes.
// On rotation the active file becomes name.1, name.1 becomes name.2, and so on
// up to keep backups; the oldest is discarded. This keeps a bounded amount of
// history on disk without a rotation dependency.
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	file     *os.File
	size     int64
}

func NewRotatingWriter(path string, maxBytes int64, keep int) (*RotatingWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &RotatingWriter{
		path:     path,
		maxBytes: maxBytes,
		keep:     keep,
		file:     file,
		size:     info.Size(),
	}, nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingWriter) rotateLocked() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	_ = os.Remove(w.backupPath(w.keep))
	for i := w.keep - 1; i >= 1; i-- {
		_ = os.Rename(w.backupPath(i), w.backupPath(i+1))
	}
	_ = os.Rename(w.path, w.backupPath(1))

	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w.file = file
	w.size = 0
	return nil
}

func (w *RotatingWriter) backupPath(index int) string {
	return w.path + "." + strconv.Itoa(index)
}
