package service

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestParseRangeAcceptsSingleByteRanges(t *testing.T) {
	for _, value := range []string{"bytes=0-", "bytes=1024-2047", "bytes=-512"} {
		t.Run(value, func(t *testing.T) {
			result, err := ParseRange(value)
			if err != nil {
				t.Fatal(err)
			}
			if result != value {
				t.Fatalf("range = %q", result)
			}
		})
	}
}

func TestParseRangeRejectsInvalidRanges(t *testing.T) {
	for _, value := range []string{"bytes=", "bytes=20-10", "bytes=-0", "bytes=0-1,4-5", "items=0-1"} {
		t.Run(value, func(t *testing.T) {
			if _, err := ParseRange(value); err == nil {
				t.Fatal("expected range to be rejected")
			}
		})
	}
}

func TestBoundedReaderRejectsOversizedStream(t *testing.T) {
	body := newBoundedReadCloser(io.NopCloser(strings.NewReader("firstsecond")), 8)
	_, err := io.ReadAll(body)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPlaybackContentTypeMapsQuickTimeToMP4(t *testing.T) {
	if result := PlaybackContentType("video/quicktime"); result != "video/mp4" {
		t.Fatalf("content type = %q", result)
	}
	if result := PlaybackContentType("audio/ogg"); result != "audio/ogg" {
		t.Fatalf("content type = %q", result)
	}
}

func TestBoundedReaderPreservesSourceError(t *testing.T) {
	expected := errors.New("source failed")
	body := newBoundedReadCloser(errorReadCloser{err: expected}, 8)
	_, err := body.Read(make([]byte, 8))
	if !errors.Is(err, expected) {
		t.Fatalf("error = %v", err)
	}
}

type errorReadCloser struct {
	err error
}

func (r errorReadCloser) Read(_ []byte) (int, error) {
	return 0, r.err
}

func (errorReadCloser) Close() error {
	return nil
}
