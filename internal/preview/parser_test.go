package preview

import (
	"net/url"
	"reflect"
	"testing"
)

func TestParseHTMLPrefersOpenGraphMetadata(t *testing.T) {
	result := ParseHTML(`
		<title>Fallback title</title>
		<meta property="og:site_name" content="Example &amp; Co">
		<meta property="og:title" content="A better title">
		<meta property="og:description" content="A concise description.">
		<meta property="og:image" content="/preview.png">
		<meta property="og:image:width" content="1200">
		<meta property="og:image:height" content="630">
		<meta property="og:image:alt" content="A preview illustration">
		<meta property="og:color" content="#1A2b3C">
	`, mustTestURL(t, "https://example.test/article"))
	expected := Preview{
		URL:         "https://example.test/article",
		Title:       "A better title",
		Description: "A concise description.",
		Provider:    &Provider{Name: "Example & Co"},
		Image:       &Media{URL: "https://example.test/preview.png", MediaType: "image/png", Width: 1200, Height: 630, Description: "A preview illustration"},
		Color:       "#1a2b3c",
	}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("unexpected preview:\n%#v\nexpected:\n%#v", result, expected)
	}
}

func TestParseHTMLBuildsSummaryCard(t *testing.T) {
	result := ParseHTML(`
		<meta property="og:title" content="A compact preview">
		<meta property="og:site_name" content="Example Journal">
		<meta property="og:image" content="/avatar.png">
		<meta property="og:image:width" content="144">
		<meta property="og:image:height" content="144">
		<meta name="twitter:card" content="summary">
		<meta name="author" content="Ada Lovelace">
		<meta property="article:published_time" content="2026-08-25T12:30:00+02:00">
		<meta name="twitter:label1" content="Reading time">
		<meta name="twitter:data1" content="4 minutes">
	`, mustTestURL(t, "https://journal.example.test/article"))
	expected := Preview{
		URL:       "https://journal.example.test/article",
		Title:     "A compact preview",
		Author:    &Author{Name: "Ada Lovelace"},
		Provider:  &Provider{Name: "Example Journal"},
		Fields:    []Field{{Name: "Reading time", Value: "4 minutes", Inline: true}},
		Thumbnail: &Media{URL: "https://journal.example.test/avatar.png", MediaType: "image/png", Width: 144, Height: 144},
		Timestamp: "2026-08-25T10:30:00.000Z",
	}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("unexpected preview:\n%#v\nexpected:\n%#v", result, expected)
	}
}

func TestParseHTMLPreservesRepeatedImageGallery(t *testing.T) {
	result := ParseHTML(`
		<meta property="og:title" content="Four photos">
		<meta property="og:image" content="https://pbs.twimg.com/media/one.jpg">
		<meta property="og:image:alt" content="First photo">
		<meta property="og:image" content="https://pbs.twimg.com/media/two.jpg">
		<meta property="og:image:alt" content="Second photo">
		<meta property="og:image" content="https://pbs.twimg.com/media/three.jpg">
		<meta property="og:image:alt" content="Third photo">
		<meta property="og:image" content="https://pbs.twimg.com/media/four.jpg">
		<meta property="og:image:alt" content="Fourth photo">
	`, mustTestURL(t, "https://x.com/clover/status/123"))
	if len(result.Images) != 4 {
		t.Fatalf("expected four images, got %d", len(result.Images))
	}
	for index, description := range []string{"First photo", "Second photo", "Third photo", "Fourth photo"} {
		if result.Images[index].Description != description {
			t.Fatalf("image %d description = %q", index, result.Images[index].Description)
		}
	}
}

func TestParseOEmbedFallsBackToThumbnail(t *testing.T) {
	result := ParseOEmbed(map[string]any{
		"type":             "photo",
		"title":            "A photo",
		"thumbnail_url":    "/thumbnail.jpg",
		"thumbnail_width":  320,
		"thumbnail_height": 180,
	}, mustTestURL(t, "https://photos.example.test/1"))
	expected := &Media{URL: "https://photos.example.test/thumbnail.jpg", Width: 320, Height: 180}
	if !reflect.DeepEqual(result.Thumbnail, expected) {
		t.Fatalf("unexpected thumbnail: %#v", result.Thumbnail)
	}
}

func TestWithProxiedImagesUsesPublicServiceURL(t *testing.T) {
	result := Preview{
		URL:   "https://example.test/article",
		Image: &Media{URL: "https://example.test/image.png"},
	}.WithProxiedImages(mustTestURL(t, "https://glimpse.0230.am"))
	if result.Image == nil {
		t.Fatal("expected an image")
	}
	if result.Image.SourceURL != "https://example.test/image.png" {
		t.Fatalf("source URL = %q", result.Image.SourceURL)
	}
	if result.Image.URL != "https://glimpse.0230.am/api/link-preview/image?url=https%3A%2F%2Fexample.test%2Fimage.png" {
		t.Fatalf("proxied URL = %q", result.Image.URL)
	}
}

func mustTestURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
