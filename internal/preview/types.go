package preview

import (
	"fmt"
	"net/url"
)

type Media struct {
	URL         string `json:"url"`
	SourceURL   string `json:"sourceURL,omitempty"`
	MediaType   string `json:"mediaType,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Description string `json:"description,omitempty"`
}

type Author struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
	Icon string `json:"icon,omitempty"`
}

type Provider struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type Footer struct {
	Text string `json:"text"`
	Icon string `json:"icon,omitempty"`
}

type Field struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type Preview struct {
	URL         string    `json:"url"`
	ResolvedURL string    `json:"resolvedURL,omitempty"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	Author      *Author   `json:"author,omitempty"`
	Provider    *Provider `json:"provider,omitempty"`
	Fields      []Field   `json:"fields,omitempty"`
	Image       *Media    `json:"image,omitempty"`
	Images      []Media   `json:"images,omitempty"`
	Thumbnail   *Media    `json:"thumbnail,omitempty"`
	Media       []Media   `json:"media,omitempty"`
	Footer      *Footer   `json:"footer,omitempty"`
	Timestamp   string    `json:"timestamp,omitempty"`
	Color       string    `json:"color,omitempty"`
}

func (p Preview) WithProxiedImages(publicURL *url.URL) Preview {
	if p.Author != nil && p.Author.Icon != "" {
		author := *p.Author
		author.Icon = proxyImageURL(publicURL, author.Icon)
		p.Author = &author
	}
	if p.Image != nil {
		image := proxyMedia(publicURL, *p.Image)
		p.Image = &image
	}
	if len(p.Images) > 0 {
		p.Images = proxyMediaCollection(publicURL, p.Images)
	}
	if p.Thumbnail != nil {
		thumbnail := proxyMedia(publicURL, *p.Thumbnail)
		p.Thumbnail = &thumbnail
	}
	if len(p.Media) > 0 {
		media := make([]Media, len(p.Media))
		copy(media, p.Media)
		for index := range media {
			if media[index].SourceURL == "" {
				media[index].SourceURL = media[index].URL
			}
		}
		p.Media = media
	}
	if p.Footer != nil && p.Footer.Icon != "" {
		footer := *p.Footer
		footer.Icon = proxyImageURL(publicURL, footer.Icon)
		p.Footer = &footer
	}
	return p
}

func proxyMediaCollection(publicURL *url.URL, values []Media) []Media {
	media := make([]Media, len(values))
	for index, value := range values {
		media[index] = proxyMedia(publicURL, value)
	}
	return media
}

func proxyMedia(publicURL *url.URL, value Media) Media {
	value.SourceURL = value.URL
	value.URL = proxyImageURL(publicURL, value.URL)
	return value
}

func proxyImageURL(publicURL *url.URL, value string) string {
	reference := &url.URL{Path: "/api/link-preview/image", RawQuery: url.Values{"url": []string{value}}.Encode()}
	return publicURL.ResolveReference(reference).String()
}

func MergeFallback(primary Preview, fallback Preview) Preview {
	if primary.Title == "" {
		primary.Title = fallback.Title
	}
	if primary.Description == "" {
		primary.Description = fallback.Description
	}
	if primary.Author == nil {
		primary.Author = fallback.Author
	}
	if primary.Provider == nil {
		primary.Provider = fallback.Provider
	}
	if len(primary.Fields) == 0 {
		primary.Fields = fallback.Fields
	}
	if primary.Image == nil {
		primary.Image = fallback.Image
	}
	if len(primary.Images) == 0 {
		primary.Images = fallback.Images
	}
	if primary.Thumbnail == nil {
		primary.Thumbnail = fallback.Thumbnail
	}
	if len(primary.Media) == 0 {
		primary.Media = fallback.Media
	}
	if primary.Footer == nil {
		primary.Footer = fallback.Footer
	}
	if primary.Timestamp == "" {
		primary.Timestamp = fallback.Timestamp
	}
	if primary.Color == "" {
		primary.Color = fallback.Color
	}
	return primary
}

func (p Preview) Validate() error {
	if p.URL == "" {
		return fmt.Errorf("preview URL is required")
	}
	return nil
}
