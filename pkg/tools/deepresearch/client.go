package deepresearch

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/genai"
)

// Client wraps the Google GenAI SDK for Deep Research API calls.
type Client struct {
	apiKey   string
	model    string
	timeout  int
	cache    *Cache
	genai    *genai.Client
}

// NewClient creates a GenAI API client.
// API key from GOOGLE_API_KEY or GEMINI_API_KEY environment variable.
// The GenAI client is created once and reused across requests.
func NewClient(model string, timeoutSeconds int) (*Client, error) {
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("GOOGLE_API_KEY or GEMINI_API_KEY environment variable required for Deep Research")
	}

	if model == "" {
		model = "gemini-2.0-flash"
	}
	if timeoutSeconds == 0 {
		timeoutSeconds = 1800
	}

	return &Client{
		apiKey:  apiKey,
		model:   model,
		timeout: timeoutSeconds,
		cache:   NewCache(),
		// genai client created lazily on first Research() call
	}, nil
}

// Close releases the underlying GenAI client resources.
func (c *Client) Close() {
	// genai.Client doesn't currently expose Close(), but we keep
	// this method for API completeness and future SDK versions.
	c.genai = nil
}

// Research executes a deep research query with optional file attachments.
// Returns (content, cacheHit, error). cacheHit indicates if result came from cache.
func (c *Client) Research(ctx context.Context, topic, outputFormat string, files []string, force bool) (string, bool, error) {
	// Check cache first
	if !force {
		if cached, ok := c.cache.Get(topic, outputFormat, c.model, files); ok {
			return cached.Content, true, nil
		}
	}

	// Create or reuse GenAI client
	if c.genai == nil {
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  c.apiKey,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			return "", false, fmt.Errorf("create GenAI client: %w", err)
		}
		c.genai = client
	}

	// Build prompt
	prompt := topic
	if outputFormat != "" {
		prompt += "\n\nOutput format: " + outputFormat
	}

	// Generate content
	result, err := c.genai.Models.GenerateContent(ctx, c.model, genai.Text(prompt), nil)
	if err != nil {
		return "", false, fmt.Errorf("GenAI generate: %w", err)
	}

	// Extract text from response using SDK helper
	content := result.Text()

	// Cache result
	c.cache.Put(topic, outputFormat, c.model, files, content)

	return content, false, nil
}

// SearchCache searches cached research by keyword.
func (c *Client) SearchCache(query string, limit int) []*CacheEntry {
	return c.cache.Search(query, limit)
}
