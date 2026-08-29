package api

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/0230am/glimpse/internal/fetcher"
	"github.com/0230am/glimpse/internal/preview"
	"github.com/0230am/glimpse/internal/service"
)

func TestPreviewReturnsCacheableEmptyResponseForRemote4xx(t *testing.T) {
	backend := &fakeBackend{previewError: &fetcher.HTTPError{Status: http.StatusForbidden}}
	response := request(t, backend, http.MethodGet, "/api/link-preview?url=https%3A%2F%2Fexample.test", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if cache := response.Header().Get("Cache-Control"); cache != missingCache {
		t.Fatalf("cache control = %q", cache)
	}
}

func TestPreviewUsesAbsoluteGlimpseImageURLs(t *testing.T) {
	backend := &fakeBackend{previewResult: preview.Preview{
		URL:   "https://example.test/article",
		Image: &preview.Media{URL: "https://example.test/image.png"},
	}}
	response := request(t, backend, http.MethodGet, "/api/link-preview?url=https%3A%2F%2Fexample.test%2Farticle", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	expected := `"url":"https://glimpse.0230.am/api/link-preview/image?url=https%3A%2F%2Fexample.test%2Fimage.png"`
	if !strings.Contains(response.Body.String(), expected) {
		t.Fatalf("response did not contain %s: %s", expected, response.Body.String())
	}
}

func TestCORSAllowsConfiguredOriginAndRejectsOthers(t *testing.T) {
	backend := &fakeBackend{previewResult: preview.Preview{URL: "https://example.test"}}
	allowed := httptest.NewRequest(http.MethodGet, "/api/link-preview?url=https%3A%2F%2Fexample.test", nil)
	allowed.Header.Set("Origin", "https://clover.0230.am")
	allowedResponse := httptest.NewRecorder()
	newHandler(t, backend).ServeHTTP(allowedResponse, allowed)
	if origin := allowedResponse.Header().Get("Access-Control-Allow-Origin"); origin != "https://clover.0230.am" {
		t.Fatalf("allowed origin = %q", origin)
	}

	blocked := httptest.NewRequest(http.MethodGet, "/api/link-preview?url=https%3A%2F%2Fexample.test", nil)
	blocked.Header.Set("Origin", "https://attacker.example")
	blockedResponse := httptest.NewRecorder()
	newHandler(t, backend).ServeHTTP(blockedResponse, blocked)
	if blockedResponse.Code != http.StatusForbidden {
		t.Fatalf("blocked status = %d", blockedResponse.Code)
	}
}

func TestMediaStreamsPartialResponseHeaders(t *testing.T) {
	backend := &fakeBackend{mediaResult: service.MediaResponse{
		Body:          io.NopCloser(strings.NewReader("media")),
		ContentType:   "video/mp4",
		Status:        http.StatusPartialContent,
		ContentLength: 5,
		ContentRange:  "bytes 1024-1028/2048",
		AcceptRanges:  "bytes",
		ETag:          `"media-1"`,
		LastModified:  "Thu, 27 Aug 2026 10:00:00 GMT",
	}}
	headers := http.Header{"Range": []string{"bytes=1024-"}}
	response := request(t, backend, http.MethodGet, "/api/link-preview/media?content=1&url=https%3A%2F%2Fmedia.example.test%2Fvideo.mp4", headers)
	if response.Code != http.StatusPartialContent {
		t.Fatalf("status = %d", response.Code)
	}
	if backend.mediaRange != "bytes=1024-" {
		t.Fatalf("forwarded range = %q", backend.mediaRange)
	}
	for name, expected := range map[string]string{
		"Content-Type":   "video/mp4",
		"Content-Length": "5",
		"Content-Range":  "bytes 1024-1028/2048",
		"Accept-Ranges":  "bytes",
		"ETag":           `"media-1"`,
		"Last-Modified":  "Thu, 27 Aug 2026 10:00:00 GMT",
	} {
		if actual := response.Header().Get(name); actual != expected {
			t.Errorf("%s = %q, expected %q", name, actual, expected)
		}
	}
	if response.Body.String() != "media" {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestMediaRejectsMultipartRangeBeforeCallingBackend(t *testing.T) {
	backend := &fakeBackend{}
	headers := http.Header{"Range": []string{"bytes=0-99,200-299"}}
	response := request(t, backend, http.MethodGet, "/api/link-preview/media?content=1&url=https%3A%2F%2Fmedia.example.test%2Fvideo.mp4", headers)
	if response.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("accept ranges = %q", response.Header().Get("Accept-Ranges"))
	}
	if backend.mediaCalls != 0 {
		t.Fatalf("backend called %d times", backend.mediaCalls)
	}
}

func TestMediaWithoutContentReturnsMetadata(t *testing.T) {
	backend := &fakeBackend{mediaInfoResult: &service.MediaInfo{MediaType: "video/mp4", Size: 1024}}
	response := request(t, backend, http.MethodGet, "/api/link-preview/media?url=https%3A%2F%2Fmedia.example.test%2Fvideo.mp4", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Body.String() != `{"mediaType":"video/mp4","size":1024}` {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func request(t *testing.T, backend Backend, method string, target string, headers http.Header) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	request.Header = headers.Clone()
	response := httptest.NewRecorder()
	newHandler(t, backend).ServeHTTP(response, request)
	return response
}

func newHandler(t *testing.T, backend Backend) http.Handler {
	t.Helper()
	publicURL, err := url.Parse("https://glimpse.0230.am")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	return New(backend, publicURL, map[string]struct{}{"https://clover.0230.am": {}}, logger)
}

type fakeBackend struct {
	previewResult   preview.Preview
	previewError    error
	mediaInfoResult *service.MediaInfo
	mediaInfoError  error
	mediaResult     service.MediaResponse
	mediaError      error
	mediaCalls      int
	mediaRange      string
}

func (b *fakeBackend) Preview(_ context.Context, _ string) (preview.Preview, error) {
	return b.previewResult, b.previewError
}

func (b *fakeBackend) PreviewImage(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", nil
}

func (b *fakeBackend) MediaInfo(_ context.Context, _ string) (*service.MediaInfo, error) {
	return b.mediaInfoResult, b.mediaInfoError
}

func (b *fakeBackend) Media(_ context.Context, _ string, rangeHeader string) (service.MediaResponse, error) {
	b.mediaCalls++
	b.mediaRange = rangeHeader
	return b.mediaResult, b.mediaError
}

func (b *fakeBackend) FullImage(_ context.Context, _ string) (service.MediaResponse, error) {
	return service.MediaResponse{}, nil
}
