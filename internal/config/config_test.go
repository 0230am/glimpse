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
