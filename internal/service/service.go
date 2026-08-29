package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/0230am/glimpse/internal/fetcher"
	"github.com/0230am/glimpse/internal/preview"
)

const (
	MaximumHTMLBytes   = 512 * 1024
	MaximumImageBytes  = 5 * 1024 * 1024
	MaximumOEmbedBytes = 128 * 1024
	MaximumMediaBytes  = 32 * 1024 * 1024
)

var (
	byteRangePattern        = regexp.MustCompile(`^bytes=([0-9]*)-([0-9]*)$`)
	contentRangePattern     = regexp.MustCompile(`^bytes ([0-9]+)-([0-9]+)/(\*|[0-9]+)$`)
	unsatisfiedRangePattern = regexp.MustCompile(`^bytes \*/(\*|[0-9]+)$`)
	twitterStatusPatterns   = []*regexp.Regexp{
		regexp.MustCompile(`^/[A-Za-z0-9_]{1,15}/status/([0-9]+)(?:/(?:photo|video)/[0-9]+)?/?$`),
		regexp.MustCompile(`^/i(?:/web)?/status/([0-9]+)/?$`),
	}
	twitterHosts = map[string]struct{}{"twitter.com": {}, "www.twitter.com": {}, "mobile.twitter.com": {}, "x.com": {}, "www.x.com": {}, "mobile.x.com": {}}
)

type Service struct {
	fetcher *fetcher.Client
}

type MediaInfo struct {
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size,omitempty"`
}

type MediaResponse struct {
	Body          io.ReadCloser
	ContentType   string
	Status        int
	ContentLength int64
	ContentRange  string
	AcceptRanges  string
	ETag          string
	LastModified  string
}

func New(fetchClient *fetcher.Client) *Service {
	return &Service{fetcher: fetchClient}
}

func (s *Service) Preview(ctx context.Context, value string) (preview.Preview, error) {
	resource, err := s.fetcher.FetchResource(ctx, value, MaximumHTMLBytes, "text/html,application/xhtml+xml;q=0.9")
	if err != nil {
		return preview.Preview{}, err
	}
	if !strings.Contains(strings.ToLower(resource.ContentType), "text/html") && !strings.Contains(strings.ToLower(resource.ContentType), "application/xhtml+xml") {
		return preview.Preview{}, errors.New("linked resource is not an HTML document")
	}
	document := string(resource.Body)
	result := preview.ParseHTML(document, resource.URL)
	if oEmbedURL := preview.FindOEmbedURL(document, resource.URL); oEmbedURL != "" {
		if oEmbed, fetchError := s.fetcher.FetchResource(ctx, oEmbedURL, MaximumOEmbedBytes, "application/json"); fetchError == nil && strings.Contains(strings.ToLower(oEmbed.ContentType), "json") {
			if value, parseError := preview.ParseJSON(oEmbed.Body); parseError == nil {
				result = preview.MergeFallback(result, preview.ParseOEmbed(value, resource.URL))
			}
		}
	}
	return s.enrichTwitterGallery(ctx, result, resource.URL), nil
}

func (s *Service) PreviewImage(ctx context.Context, value string) ([]byte, string, error) {
	resource, err := s.fetcher.FetchResource(ctx, value, MaximumImageBytes, "image/avif,image/webp,image/png,image/jpeg,image/gif")
	if err != nil {
		return nil, "", err
	}
	contentType := preview.ParseMediaContentType(resource.ContentType)
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", errors.New("preview image has an unsupported media type")
	}
	return resource.Body, contentType, nil
}

func (s *Service) MediaInfo(ctx context.Context, value string) (*MediaInfo, error) {
	response, err := s.fetcher.FetchHeaders(ctx, value, "image/avif,image/webp,image/png,image/jpeg,image/gif,video/*,audio/*")
	if err != nil {
		return nil, err
	}
	mediaType := preview.ParseMediaContentType(response.Header.Get("Content-Type"))
	if mediaType == "" {
		return nil, nil
	}
	size, _ := fetcher.ParseContentLength(response.Header.Get("Content-Length"), false)
	return &MediaInfo{MediaType: mediaType, Size: max(size, 0)}, nil
}

func (s *Service) Media(ctx context.Context, value string, rangeHeader string) (MediaResponse, error) {
	rangeHeader, err := ParseRange(rangeHeader)
	if err != nil {
		return MediaResponse{}, err
	}
	response, err := s.fetcher.FetchStream(ctx, value, "video/*,audio/*", rangeHeader)
	if err != nil {
		return MediaResponse{}, err
	}
	result := MediaResponse{Body: response.Body, Status: response.StatusCode, ContentLength: -1}
	if response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		result.ContentRange, err = parseContentRange(response.Header.Get("Content-Range"), response.StatusCode)
		if err != nil {
			response.Body.Close()
			return MediaResponse{}, err
		}
		response.Body.Close()
		result.Body = nil
		return result, nil
	}
	result.ContentType = preview.ParseMediaContentType(response.Header.Get("Content-Type"))
	if result.ContentType == "" || strings.HasPrefix(result.ContentType, "image/") {
		response.Body.Close()
		return MediaResponse{}, errors.New("linked media has an unsupported media type")
	}
	result.ContentLength, err = fetcher.ParseContentLength(response.Header.Get("Content-Length"), true)
	if err != nil {
		response.Body.Close()
		return MediaResponse{}, err
	}
	result.ContentRange, err = parseContentRange(response.Header.Get("Content-Range"), response.StatusCode)
	if err != nil {
		response.Body.Close()
		return MediaResponse{}, err
	}
	total := result.ContentLength
	if result.ContentRange != "" {
		total = contentRangeTotal(result.ContentRange)
	}
	if total > MaximumMediaBytes {
		response.Body.Close()
		return MediaResponse{}, errors.New("linked media is too large to preview")
	}
	if response.StatusCode == http.StatusPartialContent && result.ContentLength >= 0 && result.ContentRange != "" && result.ContentLength != contentRangeLength(result.ContentRange) {
		response.Body.Close()
		return MediaResponse{}, errors.New("linked media returned inconsistent range metadata")
	}
	if strings.EqualFold(strings.TrimSpace(response.Header.Get("Accept-Ranges")), "bytes") || response.StatusCode == http.StatusPartialContent {
		result.AcceptRanges = "bytes"
	}
	result.ETag = strings.TrimSpace(response.Header.Get("ETag"))
	result.LastModified = strings.TrimSpace(response.Header.Get("Last-Modified"))
	result.Body = newBoundedReadCloser(response.Body, MaximumMediaBytes)
	return result, nil
}

func (s *Service) FullImage(ctx context.Context, value string) (MediaResponse, error) {
	response, err := s.fetcher.FetchStream(ctx, value, "image/avif,image/webp,image/png,image/jpeg,image/gif", "")
	if err != nil {
		return MediaResponse{}, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return MediaResponse{}, errors.New("linked image returned an unexpected partial response")
	}
	contentType := preview.ParseMediaContentType(response.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "image/") {
		response.Body.Close()
		return MediaResponse{}, errors.New("linked image has an unsupported media type")
	}
	contentLength, err := fetcher.ParseContentLength(response.Header.Get("Content-Length"), true)
	if err != nil {
		response.Body.Close()
		return MediaResponse{}, err
	}
	if contentLength > MaximumMediaBytes {
		response.Body.Close()
		return MediaResponse{}, errors.New("linked image is too large to preview")
	}
	return MediaResponse{Body: newBoundedReadCloser(response.Body, MaximumMediaBytes), ContentType: contentType, Status: http.StatusOK, ContentLength: contentLength}, nil
}

func ParseRange(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	rangeValue := strings.TrimSpace(value)
	match := byteRangePattern.FindStringSubmatch(rangeValue)
	if match == nil || match[1] == "" && match[2] == "" {
		return "", &fetcher.InputError{Message: "Only one byte range can be requested."}
	}
	start, startSet, err := optionalInteger(match[1])
	if err != nil {
		return "", &fetcher.InputError{Message: "The requested media range is invalid."}
	}
	end, endSet, err := optionalInteger(match[2])
	if err != nil || startSet && endSet && start > end || !startSet && endSet && end == 0 {
		return "", &fetcher.InputError{Message: "The requested media range is invalid."}
	}
	return rangeValue, nil
}

func PlaybackContentType(value string) string {
	if value == "video/quicktime" {
		return "video/mp4"
	}
	return value
}

func (s *Service) enrichTwitterGallery(ctx context.Context, result preview.Preview, resourceURL *url.URL) preview.Preview {
	if len(result.Images) > 1 {
		return result
	}
	statusID := twitterStatusID(resourceURL)
	if statusID == "" {
		return result
	}
	resource, err := s.fetcher.FetchResource(ctx, "https://api.fxtwitter.com/2/status/"+statusID, MaximumOEmbedBytes, "application/json")
	if err != nil || !strings.Contains(strings.ToLower(resource.ContentType), "json") {
		return result
	}
	value, err := preview.ParseJSON(resource.Body)
	if err != nil {
		return result
	}
	images := preview.ParseFxTwitterGallery(value)
	if len(images) > 1 {
		result.Image = &images[0]
		result.Images = images
	}
	return result
}

func twitterStatusID(value *url.URL) string {
	if _, exists := twitterHosts[strings.ToLower(value.Hostname())]; !exists {
		return ""
	}
	for _, pattern := range twitterStatusPatterns {
		if match := pattern.FindStringSubmatch(value.Path); match != nil {
			return match[1]
		}
	}
	return ""
}

func parseContentRange(value string, status int) (string, error) {
	value = strings.TrimSpace(value)
	if status == http.StatusRequestedRangeNotSatisfiable {
		if value == "" {
			return "", nil
		}
		match := unsatisfiedRangePattern.FindStringSubmatch(value)
		if match == nil {
			return "", errors.New("linked media returned an invalid content range")
		}
		if match[1] != "*" {
			if _, err := strconv.ParseInt(match[1], 10, 64); err != nil {
				return "", errors.New("linked media returned an invalid content range")
			}
		}
		return value, nil
	}
	if status != http.StatusPartialContent {
		return "", nil
	}
	match := contentRangePattern.FindStringSubmatch(value)
	if match == nil {
		return "", errors.New("linked media returned an invalid content range")
	}
	start, startError := strconv.ParseInt(match[1], 10, 64)
	end, endError := strconv.ParseInt(match[2], 10, 64)
	if startError != nil || endError != nil || start > end {
		return "", errors.New("linked media returned an invalid content range")
	}
	if match[3] != "*" {
		total, totalError := strconv.ParseInt(match[3], 10, 64)
		if totalError != nil || total <= end {
			return "", errors.New("linked media returned an invalid content range")
		}
	}
	return value, nil
}

func contentRangeLength(value string) int64 {
	match := contentRangePattern.FindStringSubmatch(value)
	start, _ := strconv.ParseInt(match[1], 10, 64)
	end, _ := strconv.ParseInt(match[2], 10, 64)
	return end - start + 1
}

func contentRangeTotal(value string) int64 {
	match := contentRangePattern.FindStringSubmatch(value)
	if match == nil || match[3] == "*" {
		return -1
	}
	total, _ := strconv.ParseInt(match[3], 10, 64)
	return total
}

func optionalInteger(value string) (int64, bool, error) {
	if value == "" {
		return 0, false, nil
	}
	number, err := strconv.ParseInt(value, 10, 64)
	return number, true, err
}

type boundedReadCloser struct {
	source    io.ReadCloser
	remaining int64
}

func newBoundedReadCloser(source io.ReadCloser, maximum int64) io.ReadCloser {
	return &boundedReadCloser{source: source, remaining: maximum}
}

func (r *boundedReadCloser) Read(buffer []byte) (int, error) {
	if r.remaining == 0 {
		var probe [1]byte
		number, err := r.source.Read(probe[:])
		if number > 0 {
			return 0, errors.New("linked media is too large to preview")
		}
		return 0, err
	}
	if int64(len(buffer)) > r.remaining {
		buffer = buffer[:r.remaining]
	}
	number, err := r.source.Read(buffer)
	r.remaining -= int64(number)
	return number, err
}

func (r *boundedReadCloser) Close() error {
	return r.source.Close()
}

func EncodeJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}

func RemoteErrorMessage(kind string, err error) string {
	var responseError *fetcher.HTTPError
	if errors.As(err, &responseError) {
		return fmt.Sprintf("The remote %s returned HTTP %d.", kind, responseError.Status)
	}
	return ""
}
