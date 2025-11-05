package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"newsletterdigest_go/config"
)

type Client struct{}

// ChatMessage is exported for use by other packages
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatReq struct {
	Model       string          `json:"model"`
	Messages    []claudeMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens"`
	System      string          `json:"system,omitempty"`
}

type chatResp struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func NewClient() *Client {
	return &Client{}
}

func (c *Client) Chat(ctx context.Context, model string, messages []ChatMessage, temp float64, maxTok int) (string, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return "", errors.New("missing ANTHROPIC_API_KEY")
	}

	// Calling Claude API

	// Convert messages and extract system message
	var systemMsg string
	var claudeMessages []claudeMessage

	for _, msg := range messages {
		if msg.Role == "system" {
			systemMsg = msg.Content
		} else {
			claudeMessages = append(claudeMessages, claudeMessage{
				Role:    msg.Role,
				Content: msg.Content,
			})
		}
	}

	reqBody := chatReq{
		Model:       model,
		Messages:    claudeMessages,
		Temperature: temp,
		MaxTokens:   maxTok,
		System:      systemMsg,
	}

	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", strings.NewReader(string(b)))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	var lastErr error
	for attempt := 1; attempt <= config.RetryMax; attempt++ {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode == 200 {
				var cr chatResp
				if err := json.Unmarshal(body, &cr); err != nil {
					return "", err
				}
				if len(cr.Content) == 0 {
					return "", errors.New("no content in response")
				}
				return cr.Content[0].Text, nil
			}
			// Retry on 429/5xx
			if resp.StatusCode == 429 || (resp.StatusCode >= 500 && resp.StatusCode <= 599) {
				lastErr = fmt.Errorf("claude status %d: %s", resp.StatusCode, string(body))
			} else {
				return "", fmt.Errorf("claude status %d: %s", resp.StatusCode, string(body))
			}
		}

		// backoff
		sleep := time.Duration(math.Min(float64(config.BackoffMax), float64(config.BackoffMin)*math.Pow(2, float64(attempt-1)))) + time.Duration(rand.Intn(700))*time.Millisecond
		time.Sleep(sleep)
	}
	return "", lastErr
}
