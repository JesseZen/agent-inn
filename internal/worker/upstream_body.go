package worker

import (
	"io"
)

type upstreamBodyReadError struct {
	BeforeFirstByte bool
	Err             error
}

func (err *upstreamBodyReadError) Error() string {
	return err.Err.Error()
}

func (err *upstreamBodyReadError) Unwrap() error {
	return err.Err
}

type upstreamBodyReadCloser struct {
	source  io.ReadCloser
	readAny bool
}

func (body *upstreamBodyReadCloser) Read(buffer []byte) (int, error) {
	n, err := body.source.Read(buffer)
	if n > 0 {
		body.readAny = true
	}
	if err != nil && err != io.EOF {
		return n, &upstreamBodyReadError{BeforeFirstByte: !body.readAny, Err: err}
	}
	return n, err
}

func (body *upstreamBodyReadCloser) Close() error {
	return body.source.Close()
}
