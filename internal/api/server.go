package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/0230am/glimpse/internal/fetcher"
	"github.com/0230am/glimpse/internal/preview"
	"github.com/0230am/glimpse/internal/service"
)

const (
	privateCache      = "private, max-age=3600"
	missingCache      = "private, max-age=300"
	streamBufferBytes = 32 * 1024
)

type Backend interface {
	Preview(ctx context.Context, value string) (preview.Preview, error)
	PreviewImage(ctx context.Context, value string) ([]byte, string, error)
	MediaInfo(ctx context.Context, value string) (*service.MediaInfo, error)
	Media(ctx context.Context, value string, rangeHeader string) (service.MediaResponse, error)
	FullImage(ctx context.Context, value string) (service.MediaResponse, error)
}

type Server struct {
	backend        Backend
	publicURL      *url.URL
	allowedOrigins map[string]struct{}
	logger         *slog.Logger
}

func New(backend Backend, publicURL *url.URL, allowedOrigins map[string]struct{}, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	server := &Server{backend: backend, publicURL: publicURL, allowedOrigins: allowedOrigins, logger: logger}
	return server.recoverPanics(server)
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if !s.allowOrigin(writer, request) {
		writeError(writer, http.StatusForbidden, "This origin is not allowed to use Glimpse.")
		return
	}
	if request.Method == http.MethodOptions {
		writer.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		writer.Header().Set("Access-Control-Allow-Headers", "Range")
		writer.Header().Set("Access-Control-Max-Age", "86400")
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", "GET, OPTIONS")
		writeError(writer, http.StatusMethodNotAllowed, "Only GET requests are supported.")
		return
	}
	switch request.URL.Path {
	case "/livez", "/readyz":
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusNoContent)
	case "/api/link-preview":
		s.preview(writer, request)
	case "/api/link-preview/image":
		s.previewImage(writer, request)
	case "/api/link-preview/media":
		if _, present := request.URL.Query()["content"]; present {
			s.media(writer, request)
			return
		}
		s.mediaInfo(writer, request)
	case "/api/link-preview/media/image":
		s.fullImage(writer, request)
	default:
		writeError(writer, http.StatusNotFound, "Route not found.")
	}
}

func (s *Server) preview(writer http.ResponseWriter, request *http.Request) {
	value, ok := targetURL(writer, request)
	if !ok {
		return
	}
	result, err := s.backend.Preview(request.Context(), value)
	if err != nil {
		if isRemoteMissing(err) {
			writer.Header().Set("Cache-Control", missingCache)
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		s.writeBackendError(writer, "preview", err)
		return
	}
	result = result.WithProxiedImages(s.publicURL)
	writeJSON(writer, http.StatusOK, privateCache, result)
}

func (s *Server) previewImage(writer http.ResponseWriter, request *http.Request) {
	value, ok := targetURL(writer, request)
	if !ok {
		return
	}
	body, contentType, err := s.backend.PreviewImage(request.Context(), value)
	if err != nil {
		s.writeBackendError(writer, "preview image", err)
		return
	}
	writer.Header().Set("Cache-Control", privateCache)
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body)
}

func (s *Server) mediaInfo(writer http.ResponseWriter, request *http.Request) {
	value, ok := targetURL(writer, request)
	if !ok {
		return
	}
	result, err := s.backend.MediaInfo(request.Context(), value)
	if err != nil {
		s.writeBackendError(writer, "media", err)
		return
	}
	if result == nil {
		writer.Header().Set("Cache-Control", privateCache)
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(writer, http.StatusOK, privateCache, result)
}

func (s *Server) media(writer http.ResponseWriter, request *http.Request) {
	value, ok := targetURL(writer, request)
	if !ok {
		return
	}
	rangeHeader, err := service.ParseRange(request.Header.Get("Range"))
	if err != nil {
		writer.Header().Set("Accept-Ranges", "bytes")
		writeError(writer, http.StatusRequestedRangeNotSatisfiable, "The requested media range is invalid.")
		return
	}
	result, err := s.backend.Media(request.Context(), value, rangeHeader)
	if err != nil {
		s.writeBackendError(writer, "media", err)
		return
	}
	if result.Body != nil {
		defer result.Body.Close()
	}
	copyResponseHeaders(writer.Header(), result)
	writer.Header().Set("Cache-Control", privateCache)
	writer.WriteHeader(result.Status)
	if result.Body == nil {
		return
	}
	buffer := make([]byte, streamBufferBytes)
	if _, err := io.CopyBuffer(writer, result.Body, buffer); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn("media stream ended early", "error", err)
	}
}

func (s *Server) fullImage(writer http.ResponseWriter, request *http.Request) {
	value, ok := targetURL(writer, request)
	if !ok {
		return
	}
	result, err := s.backend.FullImage(request.Context(), value)
	if err != nil {
		s.writeBackendError(writer, "image", err)
		return
	}
	if result.Body != nil {
		defer result.Body.Close()
	}
	copyResponseHeaders(writer.Header(), result)
	writer.Header().Set("Cache-Control", privateCache)
	writer.WriteHeader(result.Status)
	if result.Body == nil {
		return
	}
	buffer := make([]byte, streamBufferBytes)
	if _, err := io.CopyBuffer(writer, result.Body, buffer); err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Warn("image stream ended early", "error", err)
	}
}

func (s *Server) allowOrigin(writer http.ResponseWriter, request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	writer.Header().Add("Vary", "Origin")
	if _, allowed := s.allowedOrigins[origin]; !allowed {
		return false
	}
	writer.Header().Set("Access-Control-Allow-Origin", origin)
	writer.Header().Set("Access-Control-Expose-Headers", "Accept-Ranges, Content-Length, Content-Range, Content-Type, ETag, Last-Modified")
	return true
}

func (s *Server) writeBackendError(writer http.ResponseWriter, operation string, err error) {
	var inputError *fetcher.InputError
	if errors.As(err, &inputError) {
		writeError(writer, http.StatusBadRequest, inputError.Message)
		return
	}
	s.logger.Warn("upstream request failed", "operation", operation, "error", err)
	if message := service.RemoteErrorMessage(operation, err); message != "" {
		writeError(writer, http.StatusBadGateway, message)
		return
	}
	writeError(writer, http.StatusBadGateway, "The linked "+operation+" could not be loaded.")
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				s.logger.Error("request panicked", "path", request.URL.Path, "panic", value)
				writeError(writer, http.StatusInternalServerError, "Glimpse could not process the request.")
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func copyResponseHeaders(header http.Header, result service.MediaResponse) {
	if result.ContentType != "" {
		header.Set("Content-Type", service.PlaybackContentType(result.ContentType))
	}
	if result.ContentLength >= 0 {
		header.Set("Content-Length", strconv.FormatInt(result.ContentLength, 10))
	}
	if result.ContentRange != "" {
		header.Set("Content-Range", result.ContentRange)
	}
	if result.AcceptRanges != "" {
		header.Set("Accept-Ranges", result.AcceptRanges)
	}
	if result.ETag != "" {
		header.Set("ETag", result.ETag)
	}
	if result.LastModified != "" {
		header.Set("Last-Modified", result.LastModified)
	}
}

func isRemoteMissing(err error) bool {
	var responseError *fetcher.HTTPError
	return errors.As(err, &responseError) && responseError.Status >= 400 && responseError.Status < 500
}

func targetURL(writer http.ResponseWriter, request *http.Request) (string, bool) {
	value := request.URL.Query().Get("url")
	if value == "" {
		writeError(writer, http.StatusBadRequest, "A URL is required.")
		return "", false
	}
	return value, true
}

func writeJSON(writer http.ResponseWriter, status int, cacheControl string, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "Glimpse could not encode the response.")
		return
	}
	writer.Header().Set("Cache-Control", cacheControl)
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, "no-store", map[string]string{"message": message})
}
