package fetcher

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maximumRedirects    = 4
	requestDeadline     = 7 * time.Second
	mediaDeadline       = 2 * time.Minute
	responseHeaderLimit = 64 * 1024
)

var blockedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

type InputError struct {
	Message string
}

func (e *InputError) Error() string {
	return e.Message
}

type HTTPError struct {
	Status int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("remote resource returned HTTP %d", e.Status)
}

type Resource struct {
	Body        []byte
	ContentType string
	URL         *url.URL
}

type Response struct {
	Body       io.ReadCloser
	Header     http.Header
	StatusCode int
	URL        *url.URL
}

type HeaderResponse struct {
	Header     http.Header
	StatusCode int
	URL        *url.URL
}

type Resolver interface {
	LookupNetIP(ctx context.Context, network string, host string) ([]netip.Addr, error)
}

type Config struct {
	MaximumConcurrentRequests        int
	MaximumConcurrentRequestsPerHost int
	AllowedPorts                     map[string]struct{}
	Resolver                         Resolver
}

type Client struct {
	httpClient   *http.Client
	limiter      *requestLimiter
	allowedPorts map[string]struct{}
}

func New(config Config) *Client {
	resolver := config.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if config.MaximumConcurrentRequests <= 0 {
		config.MaximumConcurrentRequests = 64
	}
	if config.MaximumConcurrentRequestsPerHost <= 0 {
		config.MaximumConcurrentRequestsPerHost = 8
	}
	allowedPorts := cloneAllowedPorts(config.AllowedPorts)
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            publicDialer(resolver),
		ForceAttemptHTTP2:      true,
		DisableCompression:     true,
		MaxIdleConns:           config.MaximumConcurrentRequests,
		MaxIdleConnsPerHost:    config.MaximumConcurrentRequestsPerHost,
		MaxConnsPerHost:        config.MaximumConcurrentRequestsPerHost,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    7 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: responseHeaderLimit,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		limiter:      newRequestLimiter(config.MaximumConcurrentRequests, config.MaximumConcurrentRequestsPerHost),
		allowedPorts: allowedPorts,
	}
}

func ValidateURL(value string) (*url.URL, error) {
	return validateURL(value, map[string]struct{}{"80": {}, "443": {}})
}

func validateURL(value string, allowedPorts map[string]struct{}) (*url.URL, error) {
	if len(value) > 4_096 {
		return nil, &InputError{Message: "The link preview URL is too long."}
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" {
		return nil, &InputError{Message: "The link preview URL is invalid."}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, &InputError{Message: "Only HTTP links can be previewed."}
	}
	if parsed.User != nil {
		return nil, &InputError{Message: "Link preview URLs cannot contain credentials."}
	}
	port := parsed.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[parsed.Scheme]
	}
	if _, allowed := allowedPorts[port]; !allowed {
		return nil, &InputError{Message: "Link preview URLs cannot use this port."}
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "" || hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") || strings.HasSuffix(hostname, ".local") || strings.HasSuffix(hostname, ".internal") || strings.HasSuffix(hostname, ".lan") {
		return nil, &InputError{Message: "Private network links cannot be previewed."}
	}
	if address, err := netip.ParseAddr(hostname); err == nil && !isPublicAddress(address) {
		return nil, &InputError{Message: "Private network links cannot be previewed."}
	}
	return parsed, nil
}

func (c *Client) FetchResource(ctx context.Context, value string, maximumBytes int64, accept string) (Resource, error) {
	requestContext, cancel := context.WithTimeout(ctx, requestDeadline)
	defer cancel()
	current, err := validateURL(value, c.allowedPorts)
	if err != nil {
		return Resource{}, err
	}
	for redirects := 0; redirects <= maximumRedirects; redirects++ {
		response, err := c.request(requestContext, http.MethodGet, current, accept, "")
		if err != nil {
			return Resource{}, err
		}
		if isRedirect(response.StatusCode) {
			location := response.Header.Get("Location")
			response.Body.Close()
			if location == "" || redirects == maximumRedirects {
				return Resource{}, errors.New("linked resource redirected too many times")
			}
			current, err = redirectURL(current, location, c.allowedPorts)
			if err != nil {
				return Resource{}, err
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			return Resource{}, &HTTPError{Status: response.StatusCode}
		}
		body, err := readBounded(response.Body, maximumBytes)
		if err != nil {
			return Resource{}, err
		}
		return Resource{Body: body, ContentType: response.Header.Get("Content-Type"), URL: current}, nil
	}
	return Resource{}, errors.New("linked resource redirected too many times")
}

func (c *Client) FetchStream(ctx context.Context, value string, accept string, rangeHeader string) (Response, error) {
	requestContext, cancel := context.WithTimeout(ctx, mediaDeadline)
	current, err := validateURL(value, c.allowedPorts)
	if err != nil {
		cancel()
		return Response{}, err
	}
	for redirects := 0; redirects <= maximumRedirects; redirects++ {
		response, err := c.request(requestContext, http.MethodGet, current, accept, rangeHeader)
		if err != nil {
			cancel()
			return Response{}, err
		}
		if isRedirect(response.StatusCode) {
			location := response.Header.Get("Location")
			response.Body.Close()
			if location == "" || redirects == maximumRedirects {
				cancel()
				return Response{}, errors.New("linked resource redirected too many times")
			}
			current, err = redirectURL(current, location, c.allowedPorts)
			if err != nil {
				cancel()
				return Response{}, err
			}
			continue
		}
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent && response.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			response.Body.Close()
			cancel()
			return Response{}, &HTTPError{Status: response.StatusCode}
		}
		response.Body = &cancelReadCloser{ReadCloser: response.Body, cancel: cancel}
		return Response{Body: response.Body, Header: response.Header, StatusCode: response.StatusCode, URL: current}, nil
	}
	cancel()
	return Response{}, errors.New("linked resource redirected too many times")
}

func (c *Client) FetchHeaders(ctx context.Context, value string, accept string) (HeaderResponse, error) {
	requestContext, cancel := context.WithTimeout(ctx, requestDeadline)
	defer cancel()
	current, err := validateURL(value, c.allowedPorts)
	if err != nil {
		return HeaderResponse{}, err
	}
	for redirects := 0; redirects <= maximumRedirects; redirects++ {
		response, err := c.request(requestContext, http.MethodHead, current, accept, "")
		if err != nil {
			return HeaderResponse{}, err
		}
		if response.StatusCode == http.StatusMethodNotAllowed || response.StatusCode == http.StatusNotImplemented {
			response.Body.Close()
			response, err = c.request(requestContext, http.MethodGet, current, accept, "bytes=0-0")
			if err != nil {
				return HeaderResponse{}, err
			}
		}
		header := response.Header.Clone()
		status := response.StatusCode
		response.Body.Close()
		if isRedirect(status) {
			location := header.Get("Location")
			if location == "" || redirects == maximumRedirects {
				return HeaderResponse{}, errors.New("linked resource redirected too many times")
			}
			current, err = redirectURL(current, location, c.allowedPorts)
			if err != nil {
				return HeaderResponse{}, err
			}
			continue
		}
		if status < 200 || status >= 300 {
			return HeaderResponse{}, &HTTPError{Status: status}
		}
		return HeaderResponse{Header: header, StatusCode: status, URL: current}, nil
	}
	return HeaderResponse{}, errors.New("linked resource redirected too many times")
}

func (c *Client) request(ctx context.Context, method string, target *url.URL, accept string, rangeHeader string) (*http.Response, error) {
	release, err := c.limiter.acquire(ctx, strings.ToLower(target.Hostname()))
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		release()
		return nil, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "Glimpse/0.1")
	if rangeHeader != "" {
		request.Header.Set("Range", rangeHeader)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		release()
		return nil, err
	}
	response.Body = &releaseReadCloser{ReadCloser: response.Body, release: release}
	return response, nil
}

func publicDialer(resolver Resolver) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 7 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		hostname, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		var addresses []netip.Addr
		if literal, parseError := netip.ParseAddr(hostname); parseError == nil {
			addresses = []netip.Addr{literal}
		} else {
			addresses, err = resolver.LookupNetIP(ctx, "ip", hostname)
			if err != nil {
				return nil, err
			}
		}
		if len(addresses) == 0 {
			return nil, &InputError{Message: "The linked hostname did not resolve."}
		}
		for _, candidate := range addresses {
			if !isPublicAddress(candidate) {
				return nil, &InputError{Message: "Private network links cannot be previewed."}
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
	}
}

func isPublicAddress(address netip.Addr) bool {
	if address.Is4In6() {
		address = address.Unmap()
	}
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, network := range blockedNetworks {
		if network.Contains(address) {
			return false
		}
	}
	return true
}

func readBounded(body io.ReadCloser, maximumBytes int64) ([]byte, error) {
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maximumBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximumBytes {
		return nil, errors.New("linked resource is too large to preview")
	}
	return data, nil
}

func redirectURL(baseURL *url.URL, location string, allowedPorts map[string]struct{}) (*url.URL, error) {
	reference, err := url.Parse(location)
	if err != nil {
		return nil, &InputError{Message: "The redirect URL is invalid."}
	}
	return validateURL(baseURL.ResolveReference(reference).String(), allowedPorts)
}

func cloneAllowedPorts(value map[string]struct{}) map[string]struct{} {
	if len(value) == 0 {
		value = map[string]struct{}{"80": {}, "443": {}}
	}
	result := make(map[string]struct{}, len(value))
	for port := range value {
		result[port] = struct{}{}
	}
	return result
}

func isRedirect(status int) bool {
	return status >= 300 && status < 400
}

type releaseReadCloser struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (r *releaseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(r.release)
	return err
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (r *cancelReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(r.cancel)
	return err
}

type hostLimiter struct {
	semaphore  chan struct{}
	references int
}

type requestLimiter struct {
	global  chan struct{}
	perHost int
	mutex   sync.Mutex
	hosts   map[string]*hostLimiter
}

func newRequestLimiter(global int, perHost int) *requestLimiter {
	return &requestLimiter{global: make(chan struct{}, global), perHost: perHost, hosts: make(map[string]*hostLimiter)}
}

func (l *requestLimiter) acquire(ctx context.Context, hostname string) (func(), error) {
	select {
	case l.global <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	l.mutex.Lock()
	host := l.hosts[hostname]
	if host == nil {
		host = &hostLimiter{semaphore: make(chan struct{}, l.perHost)}
		l.hosts[hostname] = host
	}
	host.references++
	l.mutex.Unlock()
	select {
	case host.semaphore <- struct{}{}:
	case <-ctx.Done():
		l.releaseReference(hostname, host)
		<-l.global
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			<-host.semaphore
			l.releaseReference(hostname, host)
			<-l.global
		})
	}, nil
}

func (l *requestLimiter) releaseReference(hostname string, host *hostLimiter) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	host.references--
	if host.references == 0 {
		delete(l.hosts, hostname)
	}
}

func ParseContentLength(value string, allowZero bool) (int64, error) {
	if value == "" {
		return -1, nil
	}
	number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || number < 0 || !allowZero && number == 0 {
		return -1, errors.New("linked media returned an invalid content length")
	}
	return number, nil
}
