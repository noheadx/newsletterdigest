package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"newsletterdigest_go/config"
	"newsletterdigest_go/credentials"
	"newsletterdigest_go/gmail"
	"newsletterdigest_go/models"
	"newsletterdigest_go/openai"
	"newsletterdigest_go/processor"
)

type App struct {
	cfg         *config.Config
	gmailSvc    *gmail.Service
	openaiClient *openai.Client
	processor   *processor.Processor
}

func main() {
	config.LoadEnvFile(".env")

	if handleCommand() {
		return
	}

	ctx, cancel := setupContext()
	defer cancel()

	app, err := initializeApp(ctx)
	if err != nil {
		log.Fatalf("initialization failed: %v", err)
	}

	if err := app.run(ctx); err != nil {
		log.Fatalf("application error: %v", err)
	}
}

func handleCommand() bool {
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		if err := setupCredentials(); err != nil {
			log.Fatalf("setup failed: %v", err)
		}
		return true
	}
	return false
}

func setupContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	
	go func() {
		<-c
		log.Println("[digest] Received shutdown signal, gracefully shutting down...")
		cancel()
	}()
	
	return ctx, cancel
}

func initializeApp(ctx context.Context) (*App, error) {
	if err := credentials.ValidateEnvironment(); err != nil {
		return nil, fmt.Errorf("environment validation: %w", err)
	}

	gmailSvc, err := gmail.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gmail service: %w", err)
	}

	cfg := config.Load()
	openaiClient := openai.NewClient()
	proc := processor.New(openaiClient, cfg)

	return &App{
		cfg:          cfg,
		gmailSvc:     gmailSvc,
		openaiClient: openaiClient,
		processor:    proc,
	}, nil
}

func (app *App) run(ctx context.Context) error {
	newsletters, err := app.gmailSvc.FetchNewsletters(ctx, app.cfg.GmailQuery, app.cfg.MaxResults)
	if err != nil {
		return fmt.Errorf("fetch newsletters: %w", err)
	}

	if len(newsletters) == 0 && !app.cfg.LinkedInOnlyMode {
		log.Println("[digest] No unread newsletters found and LinkedIn-only mode disabled. Exiting.")
		return nil
	}

	if len(newsletters) == 0 && app.cfg.LinkedInOnlyMode {
		log.Println("[digest] No unread newsletters found, but LinkedIn-only mode enabled. Proceeding with LinkedIn content only.")
	}

	digest, processedItems, err := app.processor.ProcessNewsletters(ctx, newsletters)
	if err != nil {
		return fmt.Errorf("process newsletters: %w", err)
	}

	subject := app.generateSubject(newsletters, processedItems)
	if err := app.gmailSvc.SendHTML(ctx, app.cfg.ToEmail, subject, digest); err != nil {
		return fmt.Errorf("send email: %w", err)
	}

	if err := app.markEmailsAsRead(ctx, processedItems); err != nil {
		log.Printf("[digest] Warning: failed to mark emails as read: %v", err)
	}

	log.Printf("[digest] %s processed=%d sent_to=%s dry_run=%v",
		time.Now().Format(time.RFC3339), len(processedItems), app.cfg.ToEmail, app.cfg.DryRun)
	
	return nil
}

func (app *App) generateSubject(newsletters []*models.Newsletter, processedItems []*models.Newsletter) string {
	if len(newsletters) > 0 && len(processedItems) > 0 {
		return "Weekly Digest - " + time.Now().Format("2006-01-02")
	}
	return "LinkedIn Industry Digest - " + time.Now().Format("2006-01-02")
}

func (app *App) markEmailsAsRead(ctx context.Context, processedItems []*models.Newsletter) error {
	if app.cfg.DryRun {
		return nil
	}
	return app.gmailSvc.MarkAsRead(ctx, processedItems)
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
