package preview

import (
	"encoding/json"
	"fmt"
	stdhtml "html"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

const maximumMediaCandidates = 12

var (
	hexColorPattern = regexp.MustCompile(`(?i)^#([0-9a-f]{3}|[0-9a-f]{6})$`)
	rgbColorPattern = regexp.MustCompile(`(?i)^rgb\(\s*([0-9]{1,3})\s*,\s*([0-9]{1,3})\s*,\s*([0-9]{1,3})\s*\)$`)
)

var imageMediaTypes = map[string]struct{}{
	"image/avif": {}, "image/gif": {}, "image/jpeg": {}, "image/png": {}, "image/webp": {},
}

var audioMediaTypes = map[string]struct{}{
	"audio/aac": {}, "audio/flac": {}, "audio/mp4": {}, "audio/mpeg": {}, "audio/ogg": {}, "audio/wav": {}, "audio/webm": {}, "audio/x-wav": {},
}

var videoMediaTypes = map[string]struct{}{
	"video/mp4": {}, "video/ogg": {}, "video/quicktime": {}, "video/webm": {}, "video/x-m4v": {},
}

type metadataEntry struct {
	key     string
	content string
}

type documentMetadata struct {
	entries []metadataEntry
	title   string
	oEmbed  string
	jsonLD  []string
}

func ParseHTML(document string, baseURL *url.URL) Preview {
	documentData := scanDocument(document)
	metadata := firstMetadataValues(documentData.entries)
	title := cleanText(firstValue(metadata, "og:title", "twitter:title"), 300)
	if title == "" {
		title = cleanText(documentData.title, 300)
	}
	description := cleanText(firstValue(metadata, "og:description", "twitter:description", "description"), 500)
	providerName := cleanText(firstValue(metadata, "og:site_name", "twitter:site"), 100)
	result := Preview{
		URL:         baseURL.String(),
		Title:       title,
		Description: description,
		Author:      previewAuthor(metadata, baseURL),
		Fields:      previewFields(metadata),
		Timestamp:   normalizeTimestamp(firstValue(metadata, "article:published_time", "og:published_time", "date")),
		Color:       normalizeColor(firstValue(metadata, "og:color", "theme-color")),
	}
	if providerName != "" {
		result.Provider = &Provider{Name: providerName}
	}
	applyPreviewMedia(&result, documentData.entries, metadata, documentData.jsonLD, baseURL)
	return result
}

func ParseOEmbed(value any, baseURL *url.URL) Preview {
	result := Preview{URL: baseURL.String()}
	object, ok := value.(map[string]any)
	if !ok {
		return result
	}
	result.Title = cleanExternalText(object["title"], 300)
	authorName := cleanExternalText(object["author_name"], 100)
	if authorName != "" {
		result.Author = &Author{Name: authorName, URL: resolveStringURL(object["author_url"], baseURL)}
	}
	providerName := cleanExternalText(object["provider_name"], 100)
	if providerName != "" {
		result.Provider = &Provider{Name: providerName, URL: resolveStringURL(object["provider_url"], baseURL)}
	}
	thumbnailURL := resolveStringURL(object["thumbnail_url"], baseURL)
	imageSet := false
	if object["type"] == "photo" {
		if imageURL := resolveStringURL(object["url"], baseURL); imageURL != "" {
			image := mediaValue(imageURL, object["width"], object["height"], "", "")
			result.Image = &image
			imageSet = true
		}
	}
	if !imageSet && thumbnailURL != "" {
		thumbnail := mediaValue(thumbnailURL, object["thumbnail_width"], object["thumbnail_height"], "", "")
		result.Thumbnail = &thumbnail
	}
	return result
}

func ParseFxTwitterGallery(value any) []Media {
	object, ok := value.(map[string]any)
	if !ok || numberValue(object["code"]) != 200 {
		return nil
	}
	status, ok := object["status"].(map[string]any)
	if !ok {
		return nil
	}
	media, ok := status["media"].(map[string]any)
	if !ok {
		return nil
	}
	photos, ok := media["photos"].([]any)
	if !ok {
		return nil
	}
	result := make([]Media, 0, 4)
	for _, candidate := range photos {
		if len(result) == 4 {
			break
		}
		photo, ok := candidate.(map[string]any)
		if !ok || photo["type"] != "photo" {
			continue
		}
		photoURL := resolveStringURL(photo["url"], mustURL("https://api.fxtwitter.com/"))
		if photoURL == "" {
			continue
		}
		result = append(result, mediaValue(photoURL, photo["width"], photo["height"], cleanExternalText(photo["altText"], 300), imageMediaTypeFromURL(photoURL)))
	}
	return result
}

func FindOEmbedURL(document string, baseURL *url.URL) string {
	value := scanDocument(document).oEmbed
	return resolveHTTPURL(value, baseURL)
}

func ParseJSON(data []byte) (any, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func scanDocument(document string) documentMetadata {
	result := documentMetadata{}
	tokenizer := xhtml.NewTokenizer(strings.NewReader(document))
	inTitle := false
	for {
		tokenType := tokenizer.Next()
		if tokenType == xhtml.ErrorToken {
			return result
		}
		token := tokenizer.Token()
		switch tokenType {
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			switch token.Data {
			case "meta":
				attributes := attributeMap(token)
				key := strings.ToLower(strings.TrimSpace(firstNonempty(attributes["property"], attributes["name"])))
				content := strings.TrimSpace(attributes["content"])
				if key != "" && content != "" {
					result.entries = append(result.entries, metadataEntry{key: key, content: content})
				}
			case "link":
				attributes := attributeMap(token)
				if strings.EqualFold(strings.TrimSpace(attributes["type"]), "application/json+oembed") && containsWord(attributes["rel"], "alternate") && result.oEmbed == "" {
					result.oEmbed = attributes["href"]
				}
			case "title":
				inTitle = true
			case "script":
				attributes := attributeMap(token)
				if strings.EqualFold(strings.TrimSpace(attributes["type"]), "application/ld+json") && tokenType == xhtml.StartTagToken {
					if tokenizer.Next() == xhtml.TextToken {
						result.jsonLD = append(result.jsonLD, stdhtml.UnescapeString(string(tokenizer.Raw())))
					}
				}
			}
		case xhtml.TextToken:
			if inTitle {
				result.title += token.Data
			}
		case xhtml.EndTagToken:
			if token.Data == "title" {
				inTitle = false
			}
		}
	}
}

func attributeMap(token xhtml.Token) map[string]string {
	attributes := make(map[string]string, len(token.Attr))
	for _, attribute := range token.Attr {
		attributes[strings.ToLower(attribute.Key)] = attribute.Val
	}
	return attributes
}

func firstMetadataValues(entries []metadataEntry) map[string]string {
	metadata := make(map[string]string)
	for _, entry := range entries {
		if _, exists := metadata[entry.key]; !exists {
			metadata[entry.key] = entry.content
		}
	}
	return metadata
}

func previewAuthor(metadata map[string]string, baseURL *url.URL) *Author {
	articleAuthor := metadata["article:author"]
	articleAuthorURL := resolveHTTPURL(articleAuthor, baseURL)
	profileName := strings.TrimSpace(strings.Join(nonempty(metadata["profile:first_name"], metadata["profile:last_name"]), " "))
	fallbackName := profileName
	if fallbackName == "" && articleAuthorURL == "" {
		fallbackName = articleAuthor
	}
	name := cleanText(firstNonempty(metadata["author"], metadata["twitter:creator"], metadata["profile:username"], fallbackName), 100)
	if name == "" {
		return nil
	}
	return &Author{
		Name: name,
		URL:  firstNonempty(resolveHTTPURL(metadata["author:url"], baseURL), articleAuthorURL),
		Icon: resolveHTTPURL(firstNonempty(metadata["author:image"], metadata["profile:image"], metadata["twitter:creator:image"]), baseURL),
	}
}

func previewFields(metadata map[string]string) []Field {
	var fields []Field
	for index := 1; index <= 4; index++ {
		name := cleanText(metadata[fmt.Sprintf("twitter:label%d", index)], 100)
		value := cleanText(metadata[fmt.Sprintf("twitter:data%d", index)], 300)
		if name != "" && value != "" {
			fields = append(fields, Field{Name: name, Value: value, Inline: true})
		}
	}
	return fields
}

func applyPreviewMedia(result *Preview, entries []metadataEntry, metadata map[string]string, jsonLD []string, baseURL *url.URL) {
	var candidates []Media
	candidates = append(candidates, metadataMediaCandidates(entries, "og:image", "image", baseURL)...)
	candidates = append(candidates, metadataMediaCandidates(entries, "twitter:image", "image", baseURL)...)
	candidates = append(candidates, metadataMediaCandidates(entries, "og:video", "video", baseURL)...)
	candidates = append(candidates, metadataMediaCandidates(entries, "twitter:player:stream", "video", baseURL)...)
	candidates = append(candidates, metadataMediaCandidates(entries, "og:audio", "audio", baseURL)...)
	candidates = append(candidates, structuredMediaCandidates(jsonLD, baseURL)...)
	candidates = uniqueMedia(candidates)

	var images []Media
	var resolved []Media
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate.MediaType, "image/") && candidate.MediaType != "image/gif" {
			images = append(images, candidate)
		}
		if candidate.MediaType == "image/gif" || strings.HasPrefix(candidate.MediaType, "video/") || strings.HasPrefix(candidate.MediaType, "audio/") {
			resolved = append(resolved, candidate)
		}
	}
	var firstImage *Media
	for index := range candidates {
		if strings.HasPrefix(candidates[index].MediaType, "image/") {
			firstImage = &candidates[index]
			break
		}
	}
	if firstImage == nil {
		result.Media = resolved
		return
	}
	card := strings.ToLower(strings.TrimSpace(metadata["twitter:card"]))
	small := firstImage.MediaType != "image/gif" && (card == "summary" || card == "player" || firstImage.Width > 0 && firstImage.Height > 0 && firstImage.Width <= 400 && firstImage.Height <= 400)
	image := *firstImage
	if small {
		result.Thumbnail = &image
	} else {
		result.Image = &image
	}
	if len(images) > 1 {
		result.Images = images
	}
	result.Media = resolved
}

func metadataMediaCandidates(entries []metadataEntry, prefix string, kind string, baseURL *url.URL) []Media {
	sources := metadataValues(entries, prefix)
	if len(sources) == 0 {
		sources = metadataValues(entries, prefix+":secure_url")
	}
	if len(sources) == 0 {
		sources = metadataValues(entries, prefix+":url")
	}
	types := metadataValues(entries, prefix+":type")
	if len(types) == 0 {
		types = metadataValues(entries, prefix+":content_type")
	}
	widths := metadataValues(entries, prefix+":width")
	heights := metadataValues(entries, prefix+":height")
	descriptions := metadataValues(entries, prefix+":alt")
	result := make([]Media, 0, len(sources))
	for index, source := range sources {
		resolved := resolveHTTPURL(source, baseURL)
		if resolved == "" {
			continue
		}
		mediaType := parsePageMediaType(indexValue(types, index))
		if mediaType == "" {
			mediaType = mediaTypeFromURL(resolved, kind)
		}
		if !strings.HasPrefix(mediaType, kind+"/") {
			continue
		}
		result = append(result, mediaValue(resolved, indexValue(widths, index), indexValue(heights, index), cleanText(indexValue(descriptions, index), 300), mediaType))
	}
	return result
}

func structuredMediaCandidates(values []string, baseURL *url.URL) []Media {
	var result []Media
	for _, raw := range values {
		value, err := ParseJSON([]byte(raw))
		if err == nil {
			collectStructuredMedia(value, baseURL, &result, 0)
		}
	}
	return result
}

func collectStructuredMedia(value any, baseURL *url.URL, result *[]Media, depth int) {
	if depth > 5 || len(*result) >= maximumMediaCandidates {
		return
	}
	switch candidate := value.(type) {
	case []any:
		for index, item := range candidate {
			if index == 24 {
				break
			}
			collectStructuredMedia(item, baseURL, result, depth+1)
		}
	case map[string]any:
		contentURL := resolveStringURL(candidate["contentUrl"], baseURL)
		kind := structuredMediaKind(candidate["@type"])
		mediaType := parsePageMediaType(stringValue(candidate["encodingFormat"]))
		if mediaType == "" && contentURL != "" && kind != "" {
			mediaType = mediaTypeFromURL(contentURL, kind)
		}
		if contentURL != "" && mediaType != "" && (kind == "" || strings.HasPrefix(mediaType, kind+"/")) {
			*result = append(*result, mediaValue(contentURL, candidate["width"], candidate["height"], "", mediaType))
		}
		count := 0
		for _, item := range candidate {
			if count == 32 {
				break
			}
			collectStructuredMedia(item, baseURL, result, depth+1)
			count++
		}
	}
}

func structuredMediaKind(value any) string {
	values := []any{value}
	if array, ok := value.([]any); ok {
		values = array
	}
	for _, candidate := range values {
		name := strings.ToLower(stringValue(candidate))
		switch name {
		case "imageobject":
			return "image"
		case "videoobject":
			return "video"
		case "audioobject":
			return "audio"
		}
	}
	return ""
}

func uniqueMedia(values []Media) []Media {
	seen := make(map[string]struct{})
	result := make([]Media, 0, len(values))
	for _, value := range values {
		key := value.URL + "\x00" + value.MediaType
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
		if len(result) == maximumMediaCandidates {
			break
		}
	}
	return result
}

func metadataValues(entries []metadataEntry, key string) []string {
	var values []string
	for _, entry := range entries {
		if entry.key == key {
			values = append(values, entry.content)
		}
	}
	return values
}

func mediaValue(mediaURL string, widthValue any, heightValue any, description string, mediaType string) Media {
	return Media{URL: mediaURL, MediaType: mediaType, Width: parseDimension(widthValue), Height: parseDimension(heightValue), Description: description}
}

func ParseMediaContentType(value string) string {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	if _, exists := imageMediaTypes[mediaType]; exists {
		return mediaType
	}
	if _, exists := audioMediaTypes[mediaType]; exists {
		return mediaType
	}
	if _, exists := videoMediaTypes[mediaType]; exists {
		return mediaType
	}
	return ""
}

func parsePageMediaType(value string) string {
	return ParseMediaContentType(value)
}

func imageMediaTypeFromURL(value string) string {
	extension := strings.ToLower(path.Ext(mustParseURL(value).Path))
	switch extension {
	case ".avif":
		return "image/avif"
	case ".gif":
		return "image/gif"
	case ".jpeg", ".jpg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func mediaTypeFromURL(value string, kind string) string {
	if kind == "image" {
		return imageMediaTypeFromURL(value)
	}
	extension := strings.ToLower(path.Ext(mustParseURL(value).Path))
	if kind == "video" {
		return map[string]string{".m4v": "video/x-m4v", ".mov": "video/quicktime", ".mp4": "video/mp4", ".ogv": "video/ogg", ".webm": "video/webm"}[extension]
	}
	return map[string]string{".aac": "audio/aac", ".flac": "audio/flac", ".m4a": "audio/mp4", ".mp3": "audio/mpeg", ".oga": "audio/ogg", ".ogg": "audio/ogg", ".wav": "audio/wav", ".webm": "audio/webm"}[extension]
}

func parseDimension(value any) int {
	var number int64
	switch candidate := value.(type) {
	case int:
		number = int64(candidate)
	case int64:
		number = candidate
	case float64:
		if candidate != float64(int64(candidate)) {
			return 0
		}
		number = int64(candidate)
	case json.Number:
		parsed, err := strconv.ParseInt(string(candidate), 10, 64)
		if err != nil {
			return 0
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(candidate), 10, 64)
		if err != nil {
			return 0
		}
		number = parsed
	default:
		return 0
	}
	if number <= 0 || number > 16_384 {
		return 0
	}
	return int(number)
}

func normalizeColor(value string) string {
	value = strings.TrimSpace(value)
	if match := hexColorPattern.FindStringSubmatch(value); match != nil {
		digits := strings.ToLower(match[1])
		if len(digits) == 3 {
			return fmt.Sprintf("#%c%c%c%c%c%c", digits[0], digits[0], digits[1], digits[1], digits[2], digits[2])
		}
		return "#" + digits
	}
	match := rgbColorPattern.FindStringSubmatch(value)
	if match == nil {
		return ""
	}
	channels := make([]int, 3)
	for index := range channels {
		channels[index], _ = strconv.Atoi(match[index+1])
		if channels[index] > 255 {
			return ""
		}
	}
	return fmt.Sprintf("rgb(%d, %d, %d)", channels[0], channels[1], channels[2])
}

func normalizeTimestamp(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	formats := []string{time.RFC3339Nano, time.RFC1123, time.RFC1123Z, "2006-01-02"}
	for _, format := range formats {
		if parsed, err := time.Parse(format, value); err == nil {
			return parsed.UTC().Format("2006-01-02T15:04:05.000Z")
		}
	}
	return ""
}

func cleanExternalText(value any, maximumLength int) string {
	return cleanText(stringValue(value), maximumLength)
}

func cleanText(value string, maximumLength int) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= maximumLength {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maximumLength-1])) + "…"
}

func resolveStringURL(value any, baseURL *url.URL) string {
	return resolveHTTPURL(stringValue(value), baseURL)
}

func resolveHTTPURL(value string, baseURL *url.URL) string {
	if value == "" || len(value) > 4_096 {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	parsed = baseURL.ResolveReference(parsed)
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.User != nil {
		return ""
	}
	return parsed.String()
}

func firstValue(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if values[key] != "" {
			return values[key]
		}
	}
	return ""
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func nonempty(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func containsWord(value string, expected string) bool {
	for _, word := range strings.Fields(strings.ToLower(value)) {
		if word == expected {
			return true
		}
	}
	return false
}

func indexValue[T any](values []T, index int) T {
	var zero T
	if index < 0 || index >= len(values) {
		return zero
	}
	return values[index]
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func numberValue(value any) int64 {
	switch candidate := value.(type) {
	case json.Number:
		number, _ := candidate.Int64()
		return number
	case float64:
		return int64(candidate)
	case int:
		return int64(candidate)
	default:
		return 0
	}
}

func mustURL(value string) *url.URL {
	parsed, err := url.Parse(value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func mustParseURL(value string) *url.URL {
	parsed, _ := url.Parse(value)
	return parsed
}
