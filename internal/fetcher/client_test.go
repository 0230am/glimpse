package fetcher

import (
	"context"
	"net/netip"
	"testing"
)

func TestValidateURLAcceptsPublicHTTPURLs(t *testing.T) {
	result, err := ValidateURL("https://example.test/path")
	if err != nil {
		t.Fatal(err)
	}
	if result.String() != "https://example.test/path" {
		t.Fatalf("URL = %q", result.String())
	}
}

func TestValidateURLAcceptsConfiguredPort(t *testing.T) {
	result, err := validateURL("https://example.test:5443/upload/image.jpg", map[string]struct{}{"80": {}, "443": {}, "5443": {}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Port() != "5443" {
		t.Fatalf("port = %q", result.Port())
	}
}

func TestValidateURLRejectsPrivateAndUnsafeURLs(t *testing.T) {
	values := []string{
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://169.254.169.254/",
		"http://[::1]/",
		"http://service.internal/",
		"http://example.test:8080/",
		"http://user:password@example.test/",
		"file:///tmp/private",
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			if _, err := ValidateURL(value); err == nil {
				t.Fatal("expected URL to be rejected")
			}
		})
	}
}

func TestPublicDialerRejectsAnyPrivateDNSAnswer(t *testing.T) {
	resolver := staticResolver{addresses: []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
		netip.MustParseAddr("10.0.0.1"),
	}}
	_, err := publicDialer(resolver)(context.Background(), "tcp", "example.test:443")
	if err == nil {
		t.Fatal("expected private DNS answer to be rejected")
	}
}

func TestPublicAddressClassification(t *testing.T) {
	tests := map[string]bool{
		"93.184.216.34":                      true,
		"2606:2800:220:1:248:1893:25c8:1946": true,
		"192.0.2.1":                          false,
		"198.51.100.1":                       false,
		"203.0.113.1":                        false,
		"2001:db8::1":                        false,
		"64:ff9b::a00:1":                     false,
		"2002:0a00:0001::":                   false,
		"3fff::1":                            false,
		"fc00::1":                            false,
		"fec0::1":                            false,
	}
	for value, expected := range tests {
		if actual := isPublicAddress(netip.MustParseAddr(value)); actual != expected {
			t.Errorf("isPublicAddress(%q) = %v, expected %v", value, actual, expected)
		}
	}
}

type staticResolver struct {
	addresses []netip.Addr
}

func (r staticResolver) LookupNetIP(_ context.Context, _ string, _ string) ([]netip.Addr, error) {
	return r.addresses, nil
}
