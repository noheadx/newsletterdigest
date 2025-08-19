// Package credentials provides secure storage for OAuth credentials and tokens
package credentials

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gmail "google.golang.org/api/gmail/v1"
)

// Store handles encrypted credential storage
type Store struct {
	baseDir    string
	passphrase string
}

// Config holds configuration for the credential store
type Config struct {
	BaseDir    string // Optional: custom storage directory
	Passphrase string // Required: encryption passphrase
}

// NewStore creates a new secure credential store
func NewStore(cfg Config) (*Store, error) {
	if cfg.Passphrase == "" {
		return nil, errors.New("passphrase is required")
	}

	baseDir := cfg.BaseDir
	if baseDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		baseDir = filepath.Join(homeDir, ".secure_newsletters")
	}

	// Ensure directory exists with proper permissions
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return nil, fmt.Errorf("create credentials dir: %w", err)
	}

	return &Store{
		baseDir:    baseDir,
		passphrase: cfg.Passphrase,
	}, nil
}

// NewStoreFromEnv creates a store using environment variables
func NewStoreFromEnv() (*Store, error) {
	cfg := Config{
		BaseDir:    os.Getenv("CREDENTIALS_DIR"),
		Passphrase: os.Getenv("CREDENTIALS_PASSPHRASE"),
	}
	return NewStore(cfg)
}

// deriveKey creates an encryption key from the passphrase
func (s *Store) deriveKey(salt []byte) []byte {
	return pbkdf2.Key([]byte(s.passphrase), salt, 100000, 32, sha256.New)
}

// encrypt encrypts data using AES-GCM
func (s *Store) encrypt(data []byte) ([]byte, error) {
	// Generate random salt
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	// Derive key from passphrase and salt
	key := s.deriveKey(salt)

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	// Create GCM
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	// Generate nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	// Encrypt
	ciphertext := gcm.Seal(nonce, nonce, data, nil)

	// Prepend salt to ciphertext
	result := append(salt, ciphertext...)
	return result, nil
}

// decrypt decrypts data using AES-GCM
func (s *Store) decrypt(data []byte) ([]byte, error) {
	if len(data) < 16 {
		return nil, errors.New("invalid encrypted data")
	}

	// Extract salt and ciphertext
	salt := data[:16]
	ciphertext := data[16:]

	// Derive key
	key := s.deriveKey(salt)

	// Create AES cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	// Create GCM
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	// Check minimum length
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	// Extract nonce and actual ciphertext
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	return plaintext, nil
}

// StoreCredentials encrypts and stores Google OAuth credentials
func (s *Store) StoreCredentials(credentials []byte) error {
	// Validate JSON format
	var cred map[string]interface{}
	if err := json.Unmarshal(credentials, &cred); err != nil {
		return fmt.Errorf("invalid credentials JSON: %w", err)
	}

	encrypted, err := s.encrypt(credentials)
	if err != nil {
		return fmt.Errorf("encrypt credentials: %w", err)
	}

	credPath := filepath.Join(s.baseDir, "credentials.enc")
	if err := os.WriteFile(credPath, encrypted, 0600); err != nil {
		return fmt.Errorf("write encrypted credentials: %w", err)
	}

	return nil
}

// LoadCredentials decrypts and loads Google OAuth credentials
func (s *Store) LoadCredentials() ([]byte, error) {
	credPath := filepath.Join(s.baseDir, "credentials.enc")
	encrypted, err := os.ReadFile(credPath)
	if err != nil {
		return nil, fmt.Errorf("read encrypted credentials: %w", err)
	}

	decrypted, err := s.decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypt credentials: %w", err)
	}

	return decrypted, nil
}

// StoreToken encrypts and stores OAuth token
func (s *Store) StoreToken(token *oauth2.Token) error {
	tokenData, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	encrypted, err := s.encrypt(tokenData)
	if err != nil {
		return fmt.Errorf("encrypt token: %w", err)
	}

	tokenPath := filepath.Join(s.baseDir, "token.enc")
	if err := os.WriteFile(tokenPath, encrypted, 0600); err != nil {
		return fmt.Errorf("write encrypted token: %w", err)
	}
	fmt.Println("Writing to", string(tokenPath))
	return nil
}

// LoadToken decrypts and loads OAuth token
func (s *Store) LoadToken() (*oauth2.Token, error) {
	tokenPath := filepath.Join(s.baseDir, "token.enc")
	encrypted, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("read encrypted token: %w", err)
	}

	decrypted, err := s.decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}

	var token oauth2.Token
	if err := json.Unmarshal(decrypted, &token); err != nil {
		return nil, fmt.Errorf("unmarshal token: %w", err)
	}

	return &token, nil
}

// GetOAuthClient returns a configured OAuth client with secure credential handling
func (s *Store) GetOAuthClient(ctx context.Context, scopes ...string) (*http.Client, error) {
	// Default scopes if none provided
	if len(scopes) == 0 {
		scopes = []string{
			gmail.GmailReadonlyScope,
			gmail.GmailModifyScope,
			gmail.GmailSendScope,
		}
	}

	// Load encrypted credentials
	credData, err := s.LoadCredentials()
	if err != nil {
		return nil, fmt.Errorf("load credentials: %w", err)
	}

	config, err := google.ConfigFromJSON(credData, scopes...)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	// Try to load existing token
	tok, err := s.LoadToken()
	if err != nil {
		// Get new token if none exists
		tok, err = s.getTokenFromWeb(ctx, config)
		if err != nil {
			return nil, err
		}
		// Store the new token securely
		if err := s.StoreToken(tok); err != nil {
			return nil, fmt.Errorf("store new token: %w", err)
		}
	}

	// Check if token needs refresh
	if !tok.Valid() {
		tokenSource := config.TokenSource(ctx, tok)
		newToken, err := tokenSource.Token()
		if err != nil {
			return nil, fmt.Errorf("refresh token: %w", err)
		}
		// Store refreshed token
		if err := s.StoreToken(newToken); err != nil {
			return nil, fmt.Errorf("store refreshed token: %w", err)
		}
		tok = newToken
	}

	return config.Client(ctx, tok), nil
}

// getTokenFromWeb handles the OAuth flow
func (s *Store) getTokenFromWeb(ctx context.Context, config *oauth2.Config) (*oauth2.Token, error) {
	// Generate secure state token
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return nil, fmt.Errorf("generate state token: %w", err)
	}
	stateToken := fmt.Sprintf("%x", stateBytes)

	authURL := config.AuthCodeURL(stateToken, oauth2.AccessTypeOffline)

	fmt.Println("Authorize this app, then paste the authorization code:")
	fmt.Println(authURL)

	var code string
	fmt.Print("Enter authorization code: ")
	if _, err := fmt.Scan(&code); err != nil {
		return nil, fmt.Errorf("read authorization code: %w", err)
	}

	// Add timeout for token exchange
	exchangeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	token, err := config.Exchange(exchangeCtx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}

	return token, nil
}

// SetupFromFile initializes the credential store from a Google credentials file
func (s *Store) SetupFromFile(credentialsPath string) error {
	credData, err := os.ReadFile(credentialsPath)
	if err != nil {
		return fmt.Errorf("read credentials file: %w", err)
	}

	// Validate credentials format
	var cred map[string]interface{}
	if err := json.Unmarshal(credData, &cred); err != nil {
		return fmt.Errorf("invalid credentials JSON: %w", err)
	}

	// Store encrypted
	if err := s.StoreCredentials(credData); err != nil {
		return fmt.Errorf("store credentials: %w", err)
	}

	return nil
}

// ValidateEnvironment checks that all required environment variables are set
func ValidateEnvironment() error {
	required := map[string]string{
		"OPENAI_API_KEY":         "OpenAI API key for summarization",
		"TO_EMAIL":               "Destination email address",
		"CREDENTIALS_PASSPHRASE": "Passphrase for credential encryption",
	}

	for env, desc := range required {
		if os.Getenv(env) == "" {
			return fmt.Errorf("required environment variable %s not set (%s)", env, desc)
		}
	}

	// Validate email format
	if _, err := mail.ParseAddress(os.Getenv("TO_EMAIL")); err != nil {
		return fmt.Errorf("invalid TO_EMAIL format: %w", err)
	}

	return nil
}

// Cleanup removes all stored credentials (for testing or reset)
func (s *Store) Cleanup() error {
	credPath := filepath.Join(s.baseDir, "credentials.enc")
	tokenPath := filepath.Join(s.baseDir, "token.enc")

	var errs []error
	if err := os.Remove(credPath); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("remove credentials: %w", err))
	}
	if err := os.Remove(tokenPath); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("remove token: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}
	return nil
}
