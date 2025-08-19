package config

import (
	"os"
	"strings"
	"time"
)

const (
	GmailQueryDefault = `label:newsletter is:unread`
	MaxResults        = 80
	PerEmailMaxChars  = 6000
	PerEmailSleep     = 1100 * time.Millisecond
	RetryMax          = 5
	BackoffMin        = 1500 * time.Millisecond
	BackoffMax        = 6 * time.Second
)

type Config struct {
	GmailQuery       string
	MaxResults       int64
	ToEmail          string
	SmallModel       string
	FinalModel       string
	DryRun           bool
	AppendSample     bool
	ShowFooter       bool
	PerEmailMaxChars int
	PerEmailSleep    time.Duration
	PromptSingle     string
	PromptFinal      string
}

func Load() *Config {
	// Load .env file (optional, environment variables take precedence)
	LoadEnvFile(".env")

	// Default prompts
	defaultSinglePrompt := "You summarize single newsletters for a product executive. Return 3–6 short bullets as plain text lines (no HTML/Markdown). Priorities: 1) Product Management 2) Healthcare 3) Architecture 4) Team Organization. Compress or omit pure AI hype unless it clearly impacts those four. No fluff."

	defaultFinalPrompt := "You assemble a concise weekly digest for a product executive. Priorities: 1) Product Management 2) Healthcare 3) Architecture 4) Team Organization. AI is lowest priority; include only if clearly relevant. Keep key figures and decisions. OUTPUT ONLY PLAIN TEXT organized by sections. Use section headers like '=== Product Management ===' followed by bullet points as plain text lines starting with '- '. When you see [L1], [L2], etc., keep them as-is in the text for later link replacement. Do not use any HTML tags, Markdown, or special formatting. Just plain text with section headers and bullet points."

	return &Config{
		GmailQuery:       getenv("GMAIL_QUERY", GmailQueryDefault),
		MaxResults:       MaxResults,
		ToEmail:          os.Getenv("TO_EMAIL"),
		SmallModel:       getenv("OPENAI_MODEL_SMALL", "gpt-4o-mini"),
		FinalModel:       getenv("OPENAI_MODEL_FINAL", "gpt-4o"),
		DryRun:           strings.ToLower(getenv("DRY_RUN", "false")) == "true",
		AppendSample:     strings.ToLower(getenv("APPEND_SAMPLE", "true")) == "true",
		ShowFooter:       strings.ToLower(getenv("SHOW_FOOTER", "true")) == "true",
		PerEmailMaxChars: PerEmailMaxChars,
		PerEmailSleep:    PerEmailSleep,
		PromptSingle:     getenv("PROMPT_SINGLE_SUMMARY", defaultSinglePrompt),
		PromptFinal:      getenv("PROMPT_FINAL_SYNTHESIS", defaultFinalPrompt),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
