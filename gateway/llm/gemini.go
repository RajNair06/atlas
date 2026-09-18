package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// GeminiClient handles communication with Google's Gemini API
type GeminiClient struct {
	apiKey  string
	model   string
	client  *http.Client
	baseURL string
}

// GeminiRequest is the request body for Gemini API
type GeminiRequest struct {
	Contents         []Content        `json:"contents"`
	GenerationConfig GenerationConfig `json:"generationConfig"`
}

// Content represents a content block in Gemini request
type Content struct {
	Parts []Part `json:"parts"`
}

// Part represents a text part in Gemini request
type Part struct {
	Text string `json:"text"`
}

// GenerationConfig controls generation parameters
type GenerationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

// GeminiResponse is the response from Gemini API
type GeminiResponse struct {
	Candidates []Candidate `json:"candidates"`
}

// Candidate represents a candidate response
type Candidate struct {
	Content Content `json:"content"`
}

// NewGeminiClient creates a new Gemini API client
func NewGeminiClient(apiKey, model string, timeout time.Duration) *GeminiClient {
	if apiKey == "" {
		slog.Warn("GEMINI_API_KEY not set, healing will be disabled")
	}

	return &GeminiClient{
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: timeout},
		baseURL: "https://generativelanguage.googleapis.com/",
	}
}

// GenerateContent sends a prompt to Gemini and returns the generated text
func (g *GeminiClient) GenerateContent(prompt string) (string, error) {
	if g.apiKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY not configured")
	}

	// Build request
	reqBody := GeminiRequest{
		Contents: []Content{
			{
				Parts: []Part{
					{Text: prompt},
				},
			},
		},
		GenerationConfig: GenerationConfig{
			Temperature:     0.3, // Lower temperature for more deterministic responses
			MaxOutputTokens: 256,
		},
	}

	reqJSON, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build URL with API key as query parameter
	url := fmt.Sprintf("%sv1beta/models/%s:generateContent?key=%s",
		g.baseURL, g.model, g.apiKey)

	// Send request
	slog.Debug("calling Gemini API",
		"model", g.model,
		"prompt_length", len(prompt),
	)

	resp, err := g.client.Post(url, "application/json", bytes.NewReader(reqJSON))
	if err != nil {
		return "", fmt.Errorf("Gemini API request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Gemini API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Check if response is HTML (error page)
	if strings.HasPrefix(string(body), "<") {
		return "", fmt.Errorf("Gemini API returned HTML instead of JSON")
	}

	// Parse response
	var geminiResp GeminiResponse
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return "", fmt.Errorf("failed to parse Gemini response: %w", err)
	}

	// Extract text
	if len(geminiResp.Candidates) == 0 {
		return "", fmt.Errorf("Gemini returned empty candidates")
	}

	if len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("Gemini returned empty parts")
	}

	text := geminiResp.Candidates[0].Content.Parts[0].Text

	// Strip markdown code fences (Gemini sometimes wraps JSON in ```json```)
	text = strings.ReplaceAll(text, "```json", "")
	text = strings.ReplaceAll(text, "```", "")
	text = strings.TrimSpace(text)

	slog.Debug("Gemini response received",
		"text_length", len(text),
		"text_preview", text[:min(100, len(text))],
	)

	return text, nil
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
