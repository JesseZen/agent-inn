package manager

import (
	"net/http"
	"net/http/httptest"
	"sync"
)

type lockedResponseRecorder struct {
	mu sync.Mutex
	*httptest.ResponseRecorder
}

func newLockedResponseRecorder() *lockedResponseRecorder {
	return &lockedResponseRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *lockedResponseRecorder) Header() http.Header {
	return r.ResponseRecorder.Header()
}

func (r *lockedResponseRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ResponseRecorder.Write(p)
}

func (r *lockedResponseRecorder) WriteHeader(statusCode int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ResponseRecorder.WriteHeader(statusCode)
}

func (r *lockedResponseRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ResponseRecorder.Flush()
}

func (r *lockedResponseRecorder) BodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ResponseRecorder.Body.String()
}
