package config

import "testing"

func TestParsePublicURLAcceptsOrigin(t *testing.T) {
	result, err := parsePublicURL("https://glimpse.0230.am/")
	if err != nil {
		t.Fatal(err)
	}
	if result.String() != "https://glimpse.0230.am" {
		t.Fatalf("public URL = %q", result.String())
	}
}

func TestParsePublicURLRejectsNonOriginValues(t *testing.T) {
	for _, value := range []string{
		"https://glimpse.0230.am/path",
		"https://user@glimpse.0230.am",
		"https://glimpse.0230.am?query=1",
		"file:///tmp/glimpse",
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := parsePublicURL(value); err == nil {
				t.Fatal("expected public URL to be rejected")
			}
		})
	}
}

func TestLoadParsesAllowedPorts(t *testing.T) {
	t.Setenv("GLIMPSE_ALLOWED_PORTS", "443, 5443")
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(configuration.AllowedPorts) != 2 {
		t.Fatalf("allowed ports = %#v", configuration.AllowedPorts)
	}
	for _, port := range []string{"443", "5443"} {
		if _, allowed := configuration.AllowedPorts[port]; !allowed {
			t.Errorf("port %s is not allowed", port)
		}
	}
}

func TestLoadRejectsInvalidAllowedPorts(t *testing.T) {
	for _, value := range []string{"0", "65536", "443,", "https"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("GLIMPSE_ALLOWED_PORTS", value)
			if _, err := Load(); err == nil {
				t.Fatal("expected invalid allowed ports to be rejected")
			}
		})
	}
}
