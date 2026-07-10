// Package transcribe converts recorded speech to text via the OpenAI Whisper
// API. It is the transport behind the UI's voice-input buttons: the browser
// uploads raw audio to POST /api/transcribe, and the server relays it here so
// the OpenAI key never leaves the daemon.
package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

// DefaultBaseURL is the OpenAI transcription endpoint.
const DefaultBaseURL = "https://api.openai.com/v1/audio/transcriptions"

// Model is the Whisper model used for all transcriptions. Language is left
// unset so Whisper auto-detects it.
const Model = "whisper-1"

// Request is one transcription call.
type Request struct {
	// Key is the OpenAI API key. Required.
	Key string
	// Audio is the recorded audio, verbatim as the browser produced it.
	Audio []byte
	// ContentType is the browser's blob type (e.g. "audio/webm;codecs=opus").
	// It picks the filename extension OpenAI uses to detect the container.
	ContentType string
	// BaseURL overrides DefaultBaseURL in tests. Empty means the real API.
	BaseURL string
}

// UpstreamError is a non-2xx response from OpenAI, preserving the HTTP status
// so the API handler can map it (401 -> bad key, 429 -> rate limit, ...).
type UpstreamError struct {
	Status  int
	Message string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("openai transcription: status %d: %s", e.Status, e.Message)
}

// Transcribe uploads audio to the Whisper API and returns the recognized text.
func Transcribe(ctx context.Context, client *http.Client, req Request) (string, error) {
	url := req.BaseURL
	if url == "" {
		url = DefaultBaseURL
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("model", Model); err != nil {
		return "", fmt.Errorf("build transcription form: %w", err)
	}
	// CreateFormFile would hardcode application/octet-stream; OpenAI keys the
	// container off the filename, but sending the real content type is safer.
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filenameFor(req.ContentType)))
	header.Set("Content-Type", baseContentType(req.ContentType))
	part, err := mw.CreatePart(header)
	if err != nil {
		return "", fmt.Errorf("build transcription form: %w", err)
	}
	if _, err := part.Write(req.Audio); err != nil {
		return "", fmt.Errorf("build transcription form: %w", err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("build transcription form: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", fmt.Errorf("build transcription request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+req.Key)
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call transcription api: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read transcription response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &UpstreamError{Status: resp.StatusCode, Message: errorMessage(body)}
	}

	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("parse transcription response: %w", err)
	}
	return out.Text, nil
}

// errorMessage extracts OpenAI's {"error":{"message":...}} shape, falling back
// to the raw body so an unexpected error page is still diagnosable.
func errorMessage(body []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 300 {
		msg = msg[:300]
	}
	if msg == "" {
		msg = "no error detail"
	}
	return msg
}

// baseContentType strips codec parameters: "audio/webm;codecs=opus" -> "audio/webm".
func baseContentType(contentType string) string {
	base, _, _ := strings.Cut(contentType, ";")
	base = strings.TrimSpace(strings.ToLower(base))
	if base == "" {
		return "application/octet-stream"
	}
	return base
}

// filenameFor maps the browser's recording content type to a filename whose
// extension OpenAI accepts. MediaRecorder produces audio/webm on Chrome and
// Firefox and audio/mp4 on iOS Safari.
func filenameFor(contentType string) string {
	switch baseContentType(contentType) {
	case "audio/mp4", "video/mp4":
		return "audio.mp4"
	case "audio/mpeg", "audio/mp3":
		return "audio.mp3"
	case "audio/ogg", "application/ogg":
		return "audio.ogg"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "audio.wav"
	case "audio/m4a", "audio/x-m4a":
		return "audio.m4a"
	case "audio/flac":
		return "audio.flac"
	default:
		return "audio.webm"
	}
}
