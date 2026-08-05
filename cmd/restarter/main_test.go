package main

import (
	"os"
	"testing"
	"time"
)

func TestGetEnv(t *testing.T) {
	const key = "RESTARTER_TEST_GET_ENV"
	t.Cleanup(func() { _ = os.Unsetenv(key) })

	if got := getEnv(key, "fallback"); got != "fallback" {
		t.Errorf("getEnv() unset = %q, want fallback", got)
	}

	if err := os.Setenv(key, "from-env"); err != nil {
		t.Fatal(err)
	}
	if got := getEnv(key, "fallback"); got != "from-env" {
		t.Errorf("getEnv() set = %q, want from-env", got)
	}
}

func TestParseDurationOrDefault(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		fallback time.Duration
		want     time.Duration
	}{
		{name: "valid", input: "10s", fallback: 5 * time.Second, want: 10 * time.Second},
		{name: "invalid", input: "not-a-duration", fallback: 5 * time.Second, want: 5 * time.Second},
		{name: "empty", input: "", fallback: 5 * time.Second, want: 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseDurationOrDefault(tt.input, tt.fallback); got != tt.want {
				t.Errorf("parseDurationOrDefault(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseIntOrDefault(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		fallback int
		want     int
	}{
		{name: "valid", input: "8080", fallback: 0, want: 8080},
		{name: "invalid", input: "abc", fallback: 0, want: 0},
		{name: "empty", input: "", fallback: 42, want: 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseIntOrDefault(tt.input, tt.fallback); got != tt.want {
				t.Errorf("parseIntOrDefault(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
