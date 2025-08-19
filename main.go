package main

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	"newsletterdigest_go/config"
	"newsletterdigest_go/credentials"
	"newsletterdigest_go/gmail"
	"newsletterdigest_go/openai"
	"newsletterdigest_go/processor"
)

func main() {
	// Check for setup command
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if err := setupCredentials(); err != nil {
			log.Fatalf("setup failed: %v", err)
		}
		return
	}

	// Validate environment
	if err := credentials.ValidateEnvironment(); err != nil {
		log.Fatalf("environment validation failed: %v", err)
	}

	ctx := context.Background()

	// Initialize Gmail service
	gmailSvc, err := gmail.NewService(ctx)
	if err != nil {
		log.Fatalf("gmail service: %v", err)
	}

	// Get configuration
	cfg := config.Load()

	// Fetch newsletters
	newsletters, err := gmailSvc.FetchNewsletters(ctx, cfg.GmailQuery, cfg.MaxResults)
	if err != nil {
		log.Fatalf("fetch newsletters: %v", err)
	}

	if len(newsletters) == 0 {
		log.Println("[digest] No unread newsletters found. Exiting.")
		return
	}

	// Initialize OpenAI client
	openaiClient := openai.NewClient()

	// Process newsletters
	proc := processor.New(openaiClient, cfg)
	digest, processedItems, err := proc.ProcessNewsletters(ctx, newsletters)
	if err != nil {
		log.Fatalf("process newsletters: %v", err)
	}

	// Send digest email
	subject := "Weekly Digest - " + time.Now().Format("2006-01-02")
	if err := gmailSvc.SendHTML(ctx, cfg.ToEmail, subject, digest); err != nil {
		log.Fatalf("send email: %v", err)
	}

	// Mark emails as read (unless dry run)
	if !cfg.DryRun {
		if err := gmailSvc.MarkAsRead(ctx, processedItems); err != nil {
			log.Printf("mark as read: %v", err)
		}
	}

	log.Printf("[digest] %s processed=%d sent_to=%s dry_run=%v append=%v",
		time.Now().Format(time.RFC3339), len(processedItems), cfg.ToEmail, cfg.DryRun, cfg.AppendSample)
}

func setupCredentials() error {
	store, err := credentials.NewStoreFromEnv()
	if err != nil {
		return err
	}

	credPath := os.Getenv("GOOGLE_CREDENTIALS_FILE")
	if credPath == "" {
		return errors.New("GOOGLE_CREDENTIALS_FILE environment variable required for setup")
	}

	if err := store.SetupFromFile(credPath); err != nil {
		return err
	}

	log.Println("Credentials stored securely!")
	log.Println("You can now delete the original credentials.json file")
	log.Println("Set CREDENTIALS_PASSPHRASE environment variable for future runs")
	return nil
}
