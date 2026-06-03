package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/axiom-studio/skills.sdk/executor"
	"github.com/axiom-studio/skills.sdk/grpc"
	"github.com/axiom-studio/skills.sdk/resolver"
)

const (
	iconBuiltin = "zap"
)

var httpClient = &http.Client{
	Timeout: 60 * time.Second,
}

// ============================================================================
// MAIN
// ============================================================================

func main() {
	port := os.Getenv("SKILL_PORT")
	if port == "" {
		port = "50130"
	}

	server := grpc.NewSkillServer("skill-builtin", "1.0.0")

	server.RegisterExecutorWithSchema("fetch-url", &FetchURLExecutor{}, FetchURLSchema)
	server.RegisterExecutorWithSchema("download-file", &DownloadFileExecutor{}, DownloadFileSchema)

	fmt.Printf("Starting skill-builtin gRPC server on port %s\n", port)
	if err := server.Serve(port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to serve: %v\n", err)
		os.Exit(1)
	}
}

// ============================================================================
// CONFIG HELPERS
// ============================================================================

func getString(config map[string]interface{}, key string) string {
	if v, ok := config[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt(config map[string]interface{}, key string, def int) int {
	if v, ok := config[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case string:
			if i, err := strconv.Atoi(n); err == nil {
				return i
			}
		}
	}
	return def
}

// ============================================================================
// SCHEMAS
// ============================================================================

var FetchURLSchema = resolver.NewSchemaBuilder("fetch-url").
	WithName("Fetch URL").
	WithCategory("action").
	WithIcon(iconBuiltin).
	WithDescription("Fetch the content of a web page or API endpoint and return it as readable text. Supports HTML pages (text is extracted), plain text, and JSON responses.").
	AddSection("Request Configuration").
	AddExpressionField("url", "URL",
		resolver.WithRequired(),
		resolver.WithPlaceholder("https://example.com/article"),
		resolver.WithHint("The URL to fetch"),
	).
	AddNumberField("max_length", "Max Length",
		resolver.WithDefault(8000),
		resolver.WithMinMax(100, 100000),
		resolver.WithHint("Maximum characters to return. Use smaller values for very large pages."),
	).
	EndSection().
	AddSection("Advanced Options").
	AddNumberField("timeout", "Timeout",
		resolver.WithDefault(10),
		resolver.WithMinMax(1, 60),
		resolver.WithHint("Request timeout in seconds"),
	).
	EndSection().
	Build()

var DownloadFileSchema = resolver.NewSchemaBuilder("download-file").
	WithName("Download File").
	WithCategory("action").
	WithIcon(iconBuiltin).
	WithDescription("Download a file from a URL. Returns the file as base64 data along with its filename, MIME type, and size.").
	AddSection("Download Configuration").
	AddExpressionField("url", "URL",
		resolver.WithRequired(),
		resolver.WithPlaceholder("https://example.com/document.pdf"),
		resolver.WithHint("The URL of the file to download"),
	).
	AddExpressionField("filename", "Filename",
		resolver.WithHint("Optional desired filename. If omitted, extracted from the URL or Content-Disposition header."),
	).
	AddExpressionField("mime_type", "MIME Type",
		resolver.WithHint("Optional MIME type hint (e.g. application/pdf). Auto-detected if omitted."),
	).
	EndSection().
	AddSection("Advanced Options").
	AddNumberField("timeout", "Timeout",
		resolver.WithDefault(30),
		resolver.WithMinMax(1, 300),
		resolver.WithHint("Download timeout in seconds"),
	).
	EndSection().
	Build()

// ============================================================================
// EXECUTORS
// ============================================================================

// FetchURLExecutor handles fetch-url
type FetchURLExecutor struct{}

func (e *FetchURLExecutor) Type() string { return "fetch-url" }

func (e *FetchURLExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	rawURL := getString(config, "url")
	if rawURL == "" {
		return nil, fmt.Errorf("url is required")
	}

	maxLength := getInt(config, "max_length", 8000)
	timeoutSec := getInt(config, "timeout", 10)

	parsedURL, err := url.Parse(rawURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, fmt.Errorf("invalid URL: %s", rawURL)
	}

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Axiom-Agent/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	var text string

	if strings.Contains(contentType, "application/json") {
		text = string(body)
	} else if strings.Contains(contentType, "text/") && !strings.Contains(contentType, "text/html") {
		text = string(body)
	} else {
		// Strip HTML tags for HTML or unknown content types
		text = stripHTML(string(body))
	}

	if len(text) > maxLength {
		text = text[:maxLength] + "\n... (truncated)"
	}

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":      true,
			"url":          rawURL,
			"content":      text,
			"content_type": contentType,
			"length":       len(text),
			"status_code":  resp.StatusCode,
		},
	}, nil
}

// DownloadFileExecutor handles download-file
type DownloadFileExecutor struct{}

func (e *DownloadFileExecutor) Type() string { return "download-file" }

func (e *DownloadFileExecutor) Execute(ctx context.Context, step *executor.StepDefinition, resolver executor.TemplateResolver) (*executor.StepResult, error) {
	config := resolver.ResolveMap(step.Config)

	rawURL := getString(config, "url")
	if rawURL == "" {
		return nil, fmt.Errorf("url is required")
	}

	filename := getString(config, "filename")
	mimeTypeHint := getString(config, "mime_type")
	timeoutSec := getInt(config, "timeout", 30)

	parsedURL, err := url.Parse(rawURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, fmt.Errorf("invalid URL: %s", rawURL)
	}

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Axiom-Agent/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read file data: %w", err)
	}

	// Determine filename
	if filename == "" {
		filename = extractFilename(resp, parsedURL)
	}

	// Determine MIME type
	mimeType := mimeTypeHint
	if mimeType == "" {
		mimeType = resp.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}
	// Strip charset suffix if present
	if idx := strings.Index(mimeType, ";"); idx != -1 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}

	base64Data := base64.StdEncoding.EncodeToString(data)

	return &executor.StepResult{
		Output: map[string]interface{}{
			"success":     true,
			"url":         rawURL,
			"filename":    filename,
			"mime_type":   mimeType,
			"size":        len(data),
			"data_base64": base64Data,
			"status_code": resp.StatusCode,
		},
	}, nil
}

// ============================================================================
// HELPERS
// ============================================================================

// stripHTML removes HTML tags and decodes common entities, producing plain text.
func stripHTML(html string) string {
	// Remove script and style blocks entirely
	scriptRe := regexp.MustCompile(`(?i)<script[\s\S]*?</script>`)
	styleRe := regexp.MustCompile(`(?i)<style[\s\S]*?</style>`)
	text := scriptRe.ReplaceAllString(html, "")
	text = styleRe.ReplaceAllString(text, "")

	// Replace block-level tags with newlines
	blockRe := regexp.MustCompile(`(?i)<(br|p|div|h[1-6]|li|tr|pre|blockquote)[^>]*>`)
	text = blockRe.ReplaceAllString(text, "\n")

	// Replace closing block tags with newlines
	closeBlockRe := regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|pre|blockquote)>`)
	text = closeBlockRe.ReplaceAllString(text, "\n")

	// Remove all remaining tags
	tagRe := regexp.MustCompile(`<[^>]+>`)
	text = tagRe.ReplaceAllString(text, "")

	// Decode common entities
	replacements := map[string]string{
		"&nbsp;":  " ",
		"&amp;":   "&",
		"&lt;":    "<",
		"&gt;":    ">",
		"&quot;":  `"`,
		"&#39;":   "'",
		"&apos;":  "'",
		"&ndash;": "–",
		"&mdash;": "—",
		"&hellip;": "…",
	}
	for entity, char := range replacements {
		text = strings.ReplaceAll(text, entity, char)
	}

	// Collapse multiple newlines
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
	// Collapse multiple spaces
	text = regexp.MustCompile(`[ \t]+`).ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

// extractFilename tries to get a filename from Content-Disposition header or URL path.
func extractFilename(resp *http.Response, parsedURL *url.URL) string {
	cd := resp.Header.Get("Content-Disposition")
	if cd != "" {
		if idx := strings.Index(cd, "filename="); idx != -1 {
			fn := cd[idx+9:]
			fn = strings.Trim(fn, `"'`)
			if fn != "" {
				return fn
			}
		}
	}

	base := path.Base(parsedURL.Path)
	if base != "" && base != "." && base != "/" {
		return base
	}

	return "download"
}
