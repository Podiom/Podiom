package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/transcribe"
)

// maxTranscribeBytes caps an uploaded voice recording — Whisper's own limit.
// The UI records low-bitrate mono capped at ~2 minutes, so real uploads stay
// far below this; the cap only guards against abuse.
const maxTranscribeBytes = 25 << 20 // 25 MiB

// handleTranscribe converts a voice recording to text:
//
//	POST /api/transcribe  (raw audio body, Content-Type = the recording's type)
//	  → {"text": "..."}
//
// The browser uploads whatever MediaRecorder produced; the daemon relays it to
// the OpenAI Whisper API using the key from the environment or the `voice:`
// config block, so the key never reaches a client.
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	key := s.resolveOpenAIKey()
	if key == "" {
		http.Error(w, "no OpenAI API key configured — add one under Settings → Voice input, or set voice.openai_api_key in config.yaml", http.StatusConflict)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxTranscribeBytes)
	audio, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "audio too large (max 25 MB)", http.StatusRequestEntityTooLarge)
		return
	}
	if len(audio) == 0 {
		http.Error(w, "empty audio upload", http.StatusBadRequest)
		return
	}

	text, err := transcribe.Transcribe(r.Context(), s.transcribeClient(), transcribe.Request{
		Key:         key,
		Audio:       audio,
		ContentType: r.Header.Get("Content-Type"),
		BaseURL:     s.transcribeBaseURL,
	})
	if err != nil {
		status, msg := mapTranscribeError(err)
		http.Error(w, msg, status)
		return
	}
	writeJSON(w, map[string]string{"text": text}, nil)
}

// resolveOpenAIKey picks the Whisper key: environment first (secrets kept out
// of config.yaml), then the voice config block, re-read per request so a key
// saved in Settings applies without a restart.
func (s *Server) resolveOpenAIKey() string {
	if v := strings.TrimSpace(os.Getenv("PODIOM_OPENAI_API_KEY")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); v != "" {
		return v
	}
	return s.core.GetVoice().OpenAIAPIKey
}

// transcribeClient returns the HTTP client for Whisper calls. Whisper responds
// in seconds even for minutes of audio; the timeout is generous headroom.
func (s *Server) transcribeClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}

// mapTranscribeError converts a transcription failure into the status and
// user-facing message our API returns. The OpenAI key is never echoed.
func mapTranscribeError(err error) (int, string) {
	var upstream *transcribe.UpstreamError
	if !errors.As(err, &upstream) {
		return http.StatusBadGateway, "transcription service unreachable: " + err.Error()
	}
	switch {
	case upstream.Status == http.StatusUnauthorized || upstream.Status == http.StatusForbidden:
		return http.StatusBadRequest, "OpenAI rejected the API key — check it under Settings → Voice input"
	case upstream.Status == http.StatusTooManyRequests:
		return http.StatusTooManyRequests, "OpenAI rate limit: " + upstream.Message
	case upstream.Status >= 400 && upstream.Status < 500:
		return http.StatusBadRequest, "OpenAI: " + upstream.Message
	default:
		return http.StatusBadGateway, "transcription service error: " + upstream.Message
	}
}
