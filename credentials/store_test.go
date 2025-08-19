package credentials

import (
	"os"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestSecureStore(t *testing.T) {
	// Create temporary directory for testing
	tempDir := t.TempDir()

	store, err := NewStore(Config{
		BaseDir:    tempDir,
		Passphrase: "test-passphrase-12345",
	})
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Test credential storage and retrieval
	testCreds := []byte(`{"type":"service_account","project_id":"test"}`)

	if err := store.StoreCredentials(testCreds); err != nil {
		t.Fatalf("StoreCredentials failed: %v", err)
	}

	loadedCreds, err := store.LoadCredentials()
	if err != nil {
		t.Fatalf("LoadCredentials failed: %v", err)
	}

	if string(loadedCreds) != string(testCreds) {
		t.Errorf("Loaded credentials don't match stored credentials")
	}

	// Test token storage and retrieval
	testToken := &oauth2.Token{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		Expiry:       time.Now().Add(time.Hour),
		TokenType:    "Bearer",
	}

	if err := store.StoreToken(testToken); err != nil {
		t.Fatalf("StoreToken failed: %v", err)
	}

	loadedToken, err := store.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken failed: %v", err)
	}

	if loadedToken.AccessToken != testToken.AccessToken {
		t.Errorf("Loaded token doesn't match stored token")
	}
}

func TestEncryptionDecryption(t *testing.T) {
	store, err := NewStore(Config{
		BaseDir:    t.TempDir(),
		Passphrase: "test-passphrase-12345",
	})
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	testData := []byte("this is sensitive test data")

	encrypted, err := store.encrypt(testData)
	if err != nil {
		t.Fatalf("Encryption failed: %v", err)
	}

	if string(encrypted) == string(testData) {
		t.Error("Data was not encrypted")
	}

	decrypted, err := store.decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decryption failed: %v", err)
	}

	if string(decrypted) != string(testData) {
		t.Error("Decrypted data doesn't match original")
	}
}

func TestInvalidPassphrase(t *testing.T) {
	tempDir := t.TempDir()

	// Store with one passphrase
	store1, err := NewStore(Config{
		BaseDir:    tempDir,
		Passphrase: "correct-passphrase",
	})
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	testData := []byte(`{"test": "data"}`)
	if err := store1.StoreCredentials(testData); err != nil {
		t.Fatalf("StoreCredentials failed: %v", err)
	}

	// Try to load with different passphrase
	store2, err := NewStore(Config{
		BaseDir:    tempDir,
		Passphrase: "wrong-passphrase",
	})
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	_, err = store2.LoadCredentials()
	if err == nil {
		t.Error("Expected error when using wrong passphrase")
	}
}

func TestValidateEnvironment(t *testing.T) {
	// Save original environment
	originalValues := map[string]string{
		"OPENAI_API_KEY":         os.Getenv("OPENAI_API_KEY"),
		"TO_EMAIL":               os.Getenv("TO_EMAIL"),
		"CREDENTIALS_PASSPHRASE": os.Getenv("CREDENTIALS_PASSPHRASE"),
	}

	// Clean environment
	for key := range originalValues {
		os.Unsetenv(key)
	}

	defer func() {
		// Restore environment
		for key, value := range originalValues {
			if value != "" {
				os.Setenv(key, value)
			}
		}
	}()

	// Test missing variables
	if err := ValidateEnvironment(); err == nil {
		t.Error("Expected error when environment variables are missing")
	}

	// Test with valid variables
	os.Setenv("OPENAI_API_KEY", "test-key")
	os.Setenv("TO_EMAIL", "test@example.com")
	os.Setenv("CREDENTIALS_PASSPHRASE", "test-passphrase")

	if err := ValidateEnvironment(); err != nil {
		t.Errorf("ValidateEnvironment failed with valid environment: %v", err)
	}

	// Test invalid email
	os.Setenv("TO_EMAIL", "invalid-email")
	if err := ValidateEnvironment(); err == nil {
		t.Error("Expected error with invalid email format")
	}
}
