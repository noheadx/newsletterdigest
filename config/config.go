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
	PerEmailMaxChars int
	PerEmailSleep    time.Duration
}

func Load() *Config {
	return &Config{
		GmailQuery:       getenv("GMAIL_QUERY", GmailQueryDefault),
		MaxResults:       MaxResults,
		ToEmail:          os.Getenv("TO_EMAIL"),
		SmallModel:       getenv("OPENAI_MODEL_SMALL", "gpt-4o-mini"),
		FinalModel:       getenv("OPENAI_MODEL_FINAL", "gpt-4o"),
		DryRun:           strings.ToLower(getenv("DRY_RUN", "false")) == "true",
		AppendSample:     strings.ToLower(getenv("APPEND_SAMPLE", "true")) == "true",
		PerEmailMaxChars: PerEmailMaxChars,
		PerEmailSleep:    PerEmailSleep,
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
