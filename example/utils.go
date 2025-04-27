package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

func SetLlmApi(llm string, apiKey string) (*genai.Client, *genai.GenerativeModel, context.Context, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error creating genai client: %w", err)
	}
	model := client.GenerativeModel(llm)
	fmt.Println("LLM API setup complete.")
	return client, model, ctx, nil
}

const (
	maxRetries = 5                // Maximum number of retry attempts for LLM calls
	retryDelay = 30 * time.Second // Delay between retry attempts
)

// SentLlmPrompt sends a prompt to the LLM with retries.
// It now accepts the model and context directly.
func SentLlmPrompt(model *genai.GenerativeModel, ctx context.Context, prompt []genai.Part) string {
	if model == nil || ctx == nil {
		log.Println("SentLlmPrompt: Received nil model or context")
		return "" // Return empty string indicating an error
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		fmt.Printf("Sending prompt to LLM, attempt %d...\n", attempt+1)
		startTime := time.Now()
		resp, err := model.GenerateContent(ctx, prompt...)
		if err == nil {
			duration := time.Since(startTime)
			fmt.Printf("LLM response received in %v.\n", duration)
			var llmResponse strings.Builder // Use strings.Builder for efficiency
			for _, c := range resp.Candidates {
				if c.Content != nil {
					for _, part := range c.Content.Parts {
						if text, ok := part.(genai.Text); ok {
							llmResponse.WriteString(string(text))
						}
					}
				}
			}
			fmt.Printf("LLM prompt processed.\n")
			return llmResponse.String()
		}

		log.Printf("Error generating content (attempt %d): %v\n", attempt+1, err)

		// Check if the error is retryable (e.g., rate limit, temporary server issue)
		// This is a basic check; more specific error handling might be needed based on the genai library's errors.
		if strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "server error") {
			if attempt < maxRetries {
				fmt.Printf("Retrying in %v...\n", retryDelay)
				time.Sleep(retryDelay)
			} else {
				fmt.Printf("Max retries reached for retryable error. Aborting LLM call.\n")
				return "" // Return empty string if max retries reached
			}
		} else {
			// Non-retryable error
			fmt.Printf("Non-retryable error encountered. Aborting LLM call.\n")
			return "" // Return empty string for non-retryable errors
		}
	}
	return "" // Should not reach here, but added for completeness
}

// ParseHtmlToMarkdown converts HTML content to Markdown format.
func ParseHtmlToMarkdown(htmlContent string) (string, error) {
	converter := md.NewConverter("", true, nil)
	markdown, err := converter.ConvertString(htmlContent)
	if err != nil {
		log.Printf("Error converting HTML to Markdown: %v\n", err)
		return "", fmt.Errorf("failed to convert HTML to Markdown: %w", err)
	}
	return markdown, nil
}

// SearchWeb performs a web search for the given query using Google and Brave.
// Note: Scraping search engine results pages is generally discouraged, may violate terms of service,
// and is prone to breaking due to website structure changes. Consider using official search APIs if available.
func SearchWeb(query string) string {
	fmt.Printf("Performing web search for: %s\n", query)

	var results strings.Builder
	client := &http.Client{Timeout: 15 * time.Second} // Add a timeout

	// --- Google Search ---
	googleURL := fmt.Sprintf("https://www.google.com/search?q=%s&hl=en", url.QueryEscape(query)) // Added hl=en for consistency
	fmt.Printf("Searching Google: %s\n", googleURL)
	reqGoogle, err := http.NewRequest("GET", googleURL, nil)
	if err != nil {
		log.Printf("Error creating Google request: %v\n", err)
		results.WriteString(fmt.Sprintf("Error creating Google request: %v\n", err))
	} else {
		// Mimic a common browser User-Agent
		reqGoogle.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		googleResp, errResp := client.Do(reqGoogle)
		if errResp != nil {
			log.Printf("Error searching Google: %v\n", errResp)
			results.WriteString(fmt.Sprintf("Error searching Google: %v\n", errResp))
		} else {
			defer googleResp.Body.Close()
			if googleResp.StatusCode != http.StatusOK {
				log.Printf("Google search returned non-OK status: %s\n", googleResp.Status)
				results.WriteString(fmt.Sprintf("Google search failed with status: %s\n", googleResp.Status))
			} else {
				bodyBytes, errRead := io.ReadAll(googleResp.Body)
				if errRead != nil {
					log.Printf("Error reading Google response body: %v\n", errRead)
					results.WriteString(fmt.Sprintf("Error reading Google response: %v\n", errRead))
				} else {
					markdown, errParse := ParseHtmlToMarkdown(string(bodyBytes))
					if errParse != nil {
						results.WriteString(fmt.Sprintf("--- Google Results (Error parsing HTML: %v) ---\n", errParse))
					} else {
						results.WriteString("--- Google Results ---\n")
						results.WriteString(markdown)
						results.WriteString("\n----------------------\n")
					}
				}
			}
		}
	}

	results.WriteString("\n")

	// --- Brave Search ---
	// Note: Brave Search might have stricter anti-scraping measures.
	braveURL := fmt.Sprintf("https://search.brave.com/search?q=%s", url.QueryEscape(query))
	fmt.Printf("Searching Brave: %s\n", braveURL)
	reqBrave, err := http.NewRequest("GET", braveURL, nil)
	if err != nil {
		log.Printf("Error creating Brave request: %v\n", err)
		results.WriteString(fmt.Sprintf("Error creating Brave request: %v\n", err))
	} else {
		reqBrave.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
		braveResp, err := client.Do(reqBrave)
		if err != nil {
			log.Printf("Error searching Brave: %v\n", err)
			results.WriteString(fmt.Sprintf("Error searching Brave: %v\n", err))
		} else {
			defer braveResp.Body.Close()
			if braveResp.StatusCode != http.StatusOK {
				log.Printf("Brave search returned non-OK status: %s\n", braveResp.Status)
				results.WriteString(fmt.Sprintf("Brave search failed with status: %s\n", braveResp.Status))
			} else {
				bodyBytes, err := io.ReadAll(braveResp.Body)
				if err != nil {
					log.Printf("Error reading Brave response body: %v\n", err)
					results.WriteString(fmt.Sprintf("Error reading Brave response: %v\n", err))
				} else {
					markdown, err := ParseHtmlToMarkdown(string(bodyBytes))
					if err != nil {
						results.WriteString(fmt.Sprintf("--- Brave Results (Error parsing HTML: %v) ---\n", err))
					} else {
						results.WriteString("--- Brave Results ---\n")
						results.WriteString(markdown)
						results.WriteString("\n---------------------\n")
					}
				}
			}
		}
	}

	fmt.Println("Web search completed.")
	return results.String()
}
