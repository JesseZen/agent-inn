package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

var (
	atomicRename  = os.Rename
	atomicFsyncDir = fsyncDir
)

type Status struct {
	Generation    int
	Dirty         bool
	LastSaveError string
}

type Store struct {
	mu           sync.Mutex
	path         string
	config       Config
	status       Status
	revision     int
	writer       func(string, []byte, os.FileMode) error
	saveRequests chan struct{}
}

func LoadFile(path string) (Config, error) {
	if err := removeStaleTempFiles(path); err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.ApplyDefaults()
	return cfg, nil
}

func removeStaleTempFiles(path string) error {
	dir := filepath.Dir(path)
	pattern := filepath.Join(dir, "."+filepath.Base(path)+".tmp.*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	for _, match := range matches {
		if err := os.Remove(match); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func NewStore(path string, cfg Config) *Store {
	cfg.ApplyDefaults()
	return &Store{
		path:   path,
		config: cfg,
		status: Status{Dirty: true},
	}
}

func (s *Store) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Store) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config
}

func (s *Store) Update(fn func(*Config)) {
	s.mu.Lock()
	fn(&s.config)
	s.config.ApplyDefaults()
	s.revision++
	s.status.Dirty = true
	requests := s.saveRequests
	s.mu.Unlock()

	notifySaveRequest(requests)
}

func (s *Store) SetWriterForTest(writer func(string, []byte, os.FileMode) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writer = writer
}

func (s *Store) Save() error {
	_, err := s.saveCurrent()
	return err
}

func (s *Store) StartAsyncWriter() func() {
	requests := make(chan struct{}, 1)
	done := make(chan struct{})
	var stopOnce sync.Once

	s.mu.Lock()
	if s.saveRequests != nil {
		s.mu.Unlock()
		return func() {}
	}
	s.saveRequests = requests
	s.mu.Unlock()

	go func() {
		for {
			select {
			case <-requests:
				for {
					drainSaveRequests(requests)
					latest, err := s.saveCurrent()
					if err != nil || latest {
						break
					}
				}
			case <-done:
				return
			}
		}
	}()

	return func() {
		stopOnce.Do(func() {
			close(done)
			s.mu.Lock()
			if s.saveRequests == requests {
				s.saveRequests = nil
			}
			s.mu.Unlock()
		})
	}
}

func (s *Store) saveCurrent() (bool, error) {
	s.mu.Lock()
	s.config.ApplyDefaults()
	data, err := yaml.Marshal(s.config)
	if err != nil {
		s.markSaveErrorLocked(err)
		s.mu.Unlock()
		return false, err
	}

	writer := s.writer
	if writer == nil {
		writer = atomicWriteFile
	}
	path := s.path
	revision := s.revision
	s.mu.Unlock()

	if err := writer(path, data, 0600); err != nil {
		s.mu.Lock()
		s.markSaveErrorLocked(err)
		s.mu.Unlock()
		return false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Generation++
	latest := s.revision == revision
	s.status.Dirty = !latest
	s.status.LastSaveError = ""
	return latest, nil
}

func (s *Store) markSaveErrorLocked(err error) {
	s.status.Dirty = true
	s.status.LastSaveError = err.Error()
}

func notifySaveRequest(requests chan struct{}) {
	if requests == nil {
		return
	}
	select {
	case requests <- struct{}{}:
	default:
	}
}

func drainSaveRequests(requests chan struct{}) {
	for {
		select {
		case <-requests:
		default:
			return
		}
	}
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, defaultDirMode()); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	cleanup := func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpName)
	}

	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		cleanup()
		return err
	}
	closed = true

	if err := atomicRename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := atomicFsyncDir(dir); err != nil {
		return fmt.Errorf("fsync config directory: %w", err)
	}
	return nil
}

func fsyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func setAtomicWriteHooksForTest(_ any, rename func(string, string) error, fsync func(string) error) func() {
	prevRename := atomicRename
	prevFsync := atomicFsyncDir
	if rename != nil {
		atomicRename = rename
	}
	if fsync != nil {
		atomicFsyncDir = fsync
	}
	return func() {
		atomicRename = prevRename
		atomicFsyncDir = prevFsync
	}
}
