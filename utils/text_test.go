package utils

import (
	"testing"
)

func TestFormatEmailDate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "RFC1123Z format with timezone",
			input:    "Mon, 2 Jan 2006 15:04:05 -0700",
			expected: "Jan 2, 2006",
		},
		{
			name:     "RFC1123 format without numeric timezone",
			input:    "Mon, 2 Jan 2006 15:04:05 MST",
			expected: "Jan 2, 2006",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Typical Gmail date",
			input:    "Tue, 5 Nov 2024 08:30:00 +0000",
			expected: "Nov 5, 2024",
		},
		{
			name:     "Invalid format returns original",
			input:    "not a valid date",
			expected: "not a valid date",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatEmailDate(tt.input)
			if result != tt.expected {
				t.Errorf("FormatEmailDate(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
