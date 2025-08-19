package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadEnvFile loads environment variables from a .env file
func LoadEnvFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		// .env file is optional, so don't fail if it doesn't exist
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("error opening .env file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split on the first '=' to handle values that contain '='
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Only set if not already set (environment variables take precedence)
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}

	return scanner.Err()
}
