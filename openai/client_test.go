package openai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClaudeAPIConnection(t *testing.T) {
	// Skip if API key not set
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping integration test")
	}

	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test with a simple message
	messages := []ChatMessage{
		{Role: "user", Content: "Say 'test successful' in exactly two words."},
	}

	response, err := client.Chat(ctx, "claude-haiku-4-5-20251001", messages, 0.1, 50)
	if err != nil {
		t.Fatalf("Claude API call failed: %v", err)
	}

	if response == "" {
		t.Fatal("Expected non-empty response from Claude")
	}

	t.Logf("Claude response: %s", response)

	// Verify response contains expected content
	responseLower := strings.ToLower(response)
	if !strings.Contains(responseLower, "test") || !strings.Contains(responseLower, "successful") {
		t.Logf("Warning: Response may not match expected format: %s", response)
	}
}

func TestClaudeWithSystemMessage(t *testing.T) {
	// Skip if API key not set
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set, skipping integration test")
	}

	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test with system and user messages (like the actual app uses)
	messages := []ChatMessage{
		{Role: "system", Content: "You are a helpful assistant that responds concisely."},
		{Role: "user", Content: "What is 2+2? Answer with just the number."},
	}

	response, err := client.Chat(ctx, "claude-haiku-4-5-20251001", messages, 0.1, 50)
	if err != nil {
		t.Fatalf("Claude API call with system message failed: %v", err)
	}

	if response == "" {
		t.Fatal("Expected non-empty response from Claude")
	}

	t.Logf("Claude response: %s", response)

	// Verify response contains "4"
	if !strings.Contains(response, "4") {
		t.Logf("Warning: Expected '4' in response, got: %s", response)
	}
}
