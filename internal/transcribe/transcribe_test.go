package transcribe

import (
	"strings"
	"testing"
)

func TestBaseContentType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard with codec",
			input:    "audio/webm;codecs=opus",
			expected: "audio/webm",
		},
		{
			name:     "no codec parameter",
			input:    "audio/webm",
			expected: "audio/webm",
		},
		{
			name:     "mixed case with codecs",
			input:    "Audio/Webm;codecs=opus",
			expected: "audio/webm",
		},
		{
			name:     "whitespace around type",
			input:    "  audio/mp4 ; charset=utf-8 ",
			expected: "audio/mp4",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "application/octet-stream",
		},
		{
			name:     "only semicolon",
			input:    ";codecs=opus",
			expected: "application/octet-stream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := baseContentType(tt.input)
			if got != tt.expected {
				t.Errorf("baseContentType(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestFilenameFor(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "audio/mp4", input: "audio/mp4", expected: "audio.mp4"},
		{name: "video/mp4", input: "video/mp4", expected: "audio.mp4"},
		{name: "audio/mpeg", input: "audio/mpeg", expected: "audio.mp3"},
		{name: "audio/mp3", input: "audio/mp3", expected: "audio.mp3"},
		{name: "audio/ogg", input: "audio/ogg", expected: "audio.ogg"},
		{name: "application/ogg", input: "application/ogg", expected: "audio.ogg"},
		{name: "audio/wav", input: "audio/wav", expected: "audio.wav"},
		{name: "audio/x-wav", input: "audio/x-wav", expected: "audio.wav"},
		{name: "audio/wave", input: "audio/wave", expected: "audio.wav"},
		{name: "audio/m4a", input: "audio/m4a", expected: "audio.m4a"},
		{name: "audio/x-m4a", input: "audio/x-m4a", expected: "audio.m4a"},
		{name: "audio/flac", input: "audio/flac", expected: "audio.flac"},
		{name: "audio/webm with codecs", input: "audio/webm;codecs=opus", expected: "audio.webm"},
		{name: "unrecognized type", input: "audio/custom-unknown", expected: "audio.webm"},
		{name: "empty string", input: "", expected: "audio.webm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filenameFor(tt.input)
			if got != tt.expected {
				t.Errorf("filenameFor(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		body     []byte
		expected string
	}{
		{
			name:     "well formed error json",
			body:     []byte(`{"error":{"message":"invalid api key provided"}}`),
			expected: "invalid api key provided",
		},
		{
			name:     "valid json but no error message field",
			body:     []byte(`{"status":"failure"}`),
			expected: `{"status":"failure"}`,
		},
		{
			name:     "invalid json returns raw string",
			body:     []byte(`<html><body>Bad Gateway</body></html>`),
			expected: `<html><body>Bad Gateway</body></html>`,
		},
		{
			name:     "empty body returns default",
			body:     []byte(``),
			expected: "no error detail",
		},
		{
			name:     "whitespace body returns default",
			body:     []byte(`   
	  `),
			expected: "no error detail",
		},
		{
			name:     "long body truncated to 300 chars",
			body:     []byte(strings.Repeat("A", 400)),
			expected: strings.Repeat("A", 300),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errorMessage(tt.body)
			if got != tt.expected {
				t.Errorf("errorMessage(%q) = %q; want %q", string(tt.body), got, tt.expected)
			}
		})
	}
}
