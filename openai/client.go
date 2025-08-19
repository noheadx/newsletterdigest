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

type chatReq struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_completion_tokens"`
}

type chatResp struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
}

func NewClient() *Client {
	return &Client{}
}

func (c *Client) Chat(ctx context.Context, model string, messages []ChatMessage, temp float64, maxTok int) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("missing OPENAI_API_KEY")
	}

	fmt.Printf("Calling ChatGPT: model %s temperature: %.1f max_tokens: %d\n", model, temp, maxTok)

	reqBody := chatReq{
		Model:       model,
		Messages:    messages,
		Temperature: temp,
		MaxTokens:   maxTok,
	}

	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+apiKey)
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
				if len(cr.Choices) == 0 {
					return "", errors.New("no choices")
				}
				return cr.Choices[0].Message.Content, nil
			}
			// Retry on 429/5xx
			if resp.StatusCode == 429 || (resp.StatusCode >= 500 && resp.StatusCode <= 599) {
				lastErr = fmt.Errorf("openai status %d: %s", resp.StatusCode, string(body))
			} else {
				return "", fmt.Errorf("openai status %d: %s", resp.StatusCode, string(body))
			}
		}

		// backoff
		sleep := time.Duration(math.Min(float64(config.BackoffMax), float64(config.BackoffMin)*math.Pow(2, float64(attempt-1)))) + time.Duration(rand.Intn(700))*time.Millisecond
		time.Sleep(sleep)
	}
	return "", lastErr
}
