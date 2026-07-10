package worker

import (
	"compress/gzip"
	"compress/zlib"
	"io"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

const compressedUsageQueueSize = 4

type responseUsageObserver interface {
	Observe([]byte)
	Finish() UsageTokens
	Model() string
}

type usageObservingReadCloser struct {
	io.ReadCloser
	observer   responseUsageObserver
	compressed *compressedUsageObservation
}

type compressedUsageObservation struct {
	mu        sync.Mutex
	chunks    chan compressedUsageChunk
	result    chan responseUsageMetadata
	closed    bool
	truncated bool
}

type compressedUsageChunk struct {
	data [proxyResponseBufferSize]byte
	size int
}

type compressedUsageReader struct {
	chunks  <-chan compressedUsageChunk
	current compressedUsageChunk
	offset  int
}

func newUsageObservingReadCloser(body io.ReadCloser, contentEncoding string, observer responseUsageObserver) *usageObservingReadCloser {
	result := &usageObservingReadCloser{ReadCloser: body, observer: observer}
	encoding := strings.ToLower(strings.TrimSpace(contentEncoding))
	if encoding != contentEncodingGzip && encoding != contentEncodingDeflate && encoding != contentEncodingZstd {
		return result
	}
	result.compressed = &compressedUsageObservation{
		chunks: make(chan compressedUsageChunk, compressedUsageQueueSize),
		result: make(chan responseUsageMetadata, 1),
	}
	go result.compressed.run(encoding, observer)
	return result
}

func (r *usageObservingReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		if r.compressed == nil {
			r.observer.Observe(p[:n])
		} else {
			r.compressed.offer(p[:n])
		}
	}
	if err != nil && r.compressed != nil {
		r.compressed.finish()
	}
	return n, err
}

func (r *usageObservingReadCloser) Close() error {
	if r.compressed != nil {
		r.compressed.finish()
	}
	return r.ReadCloser.Close()
}

func (r *usageObservingReadCloser) usageResult() responseCopyResult {
	if r.compressed != nil {
		r.compressed.finish()
		select {
		case metadata := <-r.compressed.result:
			return responseCopyResult{Usage: metadata.Usage, Model: metadata.Model}
		default:
			return responseCopyResult{pending: r.compressed.result}
		}
	}
	return responseCopyResult{Usage: r.observer.Finish(), Model: r.observer.Model()}
}

func (o *compressedUsageObservation) offer(data []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	for len(data) > 0 {
		var chunk compressedUsageChunk
		chunk.size = min(len(data), len(chunk.data))
		copy(chunk.data[:chunk.size], data[:chunk.size])
		select {
		case o.chunks <- chunk:
			data = data[chunk.size:]
		default:
			o.truncated = true
			o.closed = true
			close(o.chunks)
			return
		}
	}
}

func (o *compressedUsageObservation) finish() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	o.closed = true
	close(o.chunks)
}

func (o *compressedUsageObservation) run(encoding string, observer responseUsageObserver) {
	metadata := responseUsageMetadata{Usage: UsageTokens{Known: false}}
	reader := &compressedUsageReader{chunks: o.chunks}
	var decoded io.Reader
	switch encoding {
	case contentEncodingGzip:
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			o.result <- metadata
			close(o.result)
			return
		}
		defer gzipReader.Close()
		decoded = gzipReader
	case contentEncodingDeflate:
		zlibReader, err := zlib.NewReader(reader)
		if err != nil {
			o.result <- metadata
			close(o.result)
			return
		}
		defer zlibReader.Close()
		decoded = zlibReader
	case contentEncodingZstd:
		zstdReader, err := zstd.NewReader(reader)
		if err != nil {
			o.result <- metadata
			close(o.result)
			return
		}
		defer zstdReader.Close()
		decoded = zstdReader
	}

	complete := false
	buf := make([]byte, proxyResponseBufferSize)
	for {
		n, err := decoded.Read(buf)
		if n > 0 {
			observer.Observe(buf[:n])
		}
		if err == io.EOF {
			complete = true
			break
		}
		if err != nil {
			break
		}
	}
	o.mu.Lock()
	truncated := o.truncated
	o.mu.Unlock()
	if complete && !truncated {
		metadata.Usage = observer.Finish()
		metadata.Model = observer.Model()
	}
	o.result <- metadata
	close(o.result)
}

func (r *compressedUsageReader) Read(p []byte) (int, error) {
	if r.offset == r.current.size {
		chunk, ok := <-r.chunks
		if !ok {
			return 0, io.EOF
		}
		r.current = chunk
		r.offset = 0
	}
	n := copy(p, r.current.data[r.offset:r.current.size])
	r.offset += n
	return n, nil
}
