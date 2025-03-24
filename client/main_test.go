package main

import (
	"log"
	"os"
	"testing"
)

func TestGetWebSocketURL(t *testing.T) {
	// Setup debug logger for testing
	debugLog = log.New(os.Stderr, "TEST: ", log.Ltime|log.Lshortfile)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Hostname only",
			input:    "example.com",
			expected: "wss://example.com:443/signal",
		},
		{
			name:     "Hostname with port",
			input:    "example.com:8080",
			expected: "wss://example.com:443/signal",
		},
		{
			name:     "HTTP URL",
			input:    "http://example.com",
			expected: "wss://example.com:443/signal",
		},
		{
			name:     "HTTPS URL",
			input:    "https://example.com",
			expected: "wss://example.com:443/signal",
		},
		{
			name:     "URL with path",
			input:    "https://example.com/custom",
			expected: "wss://example.com:443/custom",
		},
		{
			name:     "URL with port and path",
			input:    "https://example.com:8080/custom",
			expected: "wss://example.com:443/custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getWebSocketURL(tt.input)
			if result != tt.expected {
				t.Errorf("getWebSocketURL(%s) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseConnectionURL(t *testing.T) {
	// Setup debug logger for testing
	debugLog = log.New(os.Stderr, "TEST: ", log.Ltime|log.Lshortfile)

	tests := []struct {
		name          string
		input         string
		expectedURL   string
		expectedToken string
	}{
		{
			name:          "Empty string",
			input:         "",
			expectedURL:   "",
			expectedToken: "",
		},
		{
			name:          "Hostname only",
			input:         "example.com",
			expectedURL:   "https://example.com",
			expectedToken: "",
		},
		{
			name:          "Hostname with port",
			input:         "example.com:8080",
			expectedURL:   "https://example.com",
			expectedToken: "",
		},
		{
			name:          "URL with token",
			input:         "https://example.com/?token=abc123",
			expectedURL:   "https://example.com/",
			expectedToken: "abc123",
		},
		{
			name:          "URL with path and token",
			input:         "example.com/path?token=abc123",
			expectedURL:   "https://example.com/path",
			expectedToken: "abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, token, err := parseConnectionURL(tt.input)
			if err != nil {
				t.Errorf("parseConnectionURL(%s) returned error: %v", tt.input, err)
			}
			if url != tt.expectedURL {
				t.Errorf("parseConnectionURL(%s) URL = %s, want %s", tt.input, url, tt.expectedURL)
			}
			if token != tt.expectedToken {
				t.Errorf("parseConnectionURL(%s) token = %s, want %s", tt.input, token, tt.expectedToken)
			}
		})
	}
}
