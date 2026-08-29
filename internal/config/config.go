package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	defaultListenAddress                    = "127.0.0.1:8080"
	defaultMaximumConcurrentRequests        = 64
	defaultMaximumConcurrentRequestsPerHost = 8
)

type Config struct {
	ListenAddress                    string
	PublicURL                        *url.URL
	AllowedOrigins                   map[string]struct{}
	MaximumConcurrentRequests        int
	MaximumConcurrentRequestsPerHost int
}

func Load() (Config, error) {
	listenAddress := valueOrDefault("GLIMPSE_LISTEN_ADDRESS", defaultListenAddress)
	publicURL, err := parsePublicURL(valueOrDefault("GLIMPSE_PUBLIC_URL", "http://"+listenAddress))
	if err != nil {
		return Config{}, err
	}
	maximumConcurrentRequests, err := positiveInteger("GLIMPSE_MAX_CONCURRENT_REQUESTS", defaultMaximumConcurrentRequests)
	if err != nil {
		return Config{}, err
	}
	maximumConcurrentRequestsPerHost, err := positiveInteger("GLIMPSE_MAX_CONCURRENT_REQUESTS_PER_HOST", defaultMaximumConcurrentRequestsPerHost)
	if err != nil {
		return Config{}, err
	}
	if maximumConcurrentRequestsPerHost > maximumConcurrentRequests {
		return Config{}, fmt.Errorf("GLIMPSE_MAX_CONCURRENT_REQUESTS_PER_HOST cannot exceed GLIMPSE_MAX_CONCURRENT_REQUESTS")
	}
	return Config{
		ListenAddress:                    listenAddress,
		PublicURL:                        publicURL,
		AllowedOrigins:                   parseOrigins(os.Getenv("GLIMPSE_ALLOWED_ORIGINS")),
		MaximumConcurrentRequests:        maximumConcurrentRequests,
		MaximumConcurrentRequestsPerHost: maximumConcurrentRequestsPerHost,
	}, nil
}

func valueOrDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func parsePublicURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.User != nil || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("GLIMPSE_PUBLIC_URL must be an absolute HTTP or HTTPS origin without credentials, path, query, or fragment")
	}
	parsed.Path = ""
	return parsed, nil
}

func positiveInteger(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return number, nil
}

func parseOrigins(value string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, origin := range strings.Split(value, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			result[origin] = struct{}{}
		}
	}
	return result
}
