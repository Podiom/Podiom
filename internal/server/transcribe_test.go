package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
)

// clearOpenAIEnv keeps the ambient developer environment from satisfying the
// handler's env-first key lookup.
func clearOpenAIEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PODIOM_OPENAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
}

func TestTranscribeWithoutKeyReturnsConflict(t *testing.T) {
	clearOpenAIEnv(t)
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/transcribe", bytes.NewBufferString("audio-bytes"))
	req.Header.Set("Content-Type", "audio/webm")
	rr := httptest.NewRecorder()
	srv.handleTranscribe(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Settings") {
		t.Fatalf("error should point at Settings: %s", rr.Body.String())
	}
}

func TestTranscribeRelaysAudioToWhisper(t *testing.T) {
	clearOpenAIEnv(t)
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	srv.core.SetVoice(config.Voice{OpenAIAPIKey: "sk-test"})

	var gotAuth, gotModel, gotFilename, gotPartType string
	var gotAudio []byte
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		gotModel = r.FormValue("model")
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("form file: %v", err)
			http.Error(w, "no file", http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()
		gotFilename = header.Filename
		gotPartType = header.Header.Get("Content-Type")
		gotAudio, _ = io.ReadAll(file)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hej världen"}`))
	}))
	defer fake.Close()
	srv.transcribeBaseURL = fake.URL

	req := httptest.NewRequest(http.MethodPost, "/api/transcribe", bytes.NewBufferString("fake-opus-audio"))
	req.Header.Set("Content-Type", "audio/webm;codecs=opus")
	rr := httptest.NewRecorder()
	srv.handleTranscribe(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "hej världen") {
		t.Fatalf("text not relayed: %s", rr.Body.String())
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotModel != "whisper-1" {
		t.Fatalf("model = %q", gotModel)
	}
	if gotFilename != "audio.webm" {
		t.Fatalf("filename = %q, want audio.webm", gotFilename)
	}
	if gotPartType != "audio/webm" {
		t.Fatalf("file part content type = %q, want audio/webm (codec params stripped)", gotPartType)
	}
	if string(gotAudio) != "fake-opus-audio" {
		t.Fatalf("audio bytes not relayed verbatim: %q", gotAudio)
	}
}

func TestTranscribeMapsSafariMp4Filename(t *testing.T) {
	clearOpenAIEnv(t)
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	srv.core.SetVoice(config.Voice{OpenAIAPIKey: "sk-test"})

	var gotFilename string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			if _, header, err := r.FormFile("file"); err == nil {
				gotFilename = header.Filename
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"ok"}`))
	}))
	defer fake.Close()
	srv.transcribeBaseURL = fake.URL

	req := httptest.NewRequest(http.MethodPost, "/api/transcribe", bytes.NewBufferString("aac"))
	req.Header.Set("Content-Type", "audio/mp4")
	rr := httptest.NewRecorder()
	srv.handleTranscribe(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}
	if gotFilename != "audio.mp4" {
		t.Fatalf("filename = %q, want audio.mp4", gotFilename)
	}
}

func TestTranscribeMapsUpstreamAuthErrorWithoutEchoingKey(t *testing.T) {
	clearOpenAIEnv(t)
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	srv.core.SetVoice(config.Voice{OpenAIAPIKey: "sk-test-secret"})

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
	}))
	defer fake.Close()
	srv.transcribeBaseURL = fake.URL

	req := httptest.NewRequest(http.MethodPost, "/api/transcribe", bytes.NewBufferString("audio"))
	req.Header.Set("Content-Type", "audio/webm")
	rr := httptest.NewRecorder()
	srv.handleTranscribe(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "rejected the API key") {
		t.Fatalf("unexpected message: %s", body)
	}
	if strings.Contains(body, "sk-test-secret") {
		t.Fatalf("key echoed in error: %s", body)
	}
}

func TestTranscribeRejectsEmptyAndOversizedBodies(t *testing.T) {
	clearOpenAIEnv(t)
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	srv.core.SetVoice(config.Voice{OpenAIAPIKey: "sk-test"})

	req := httptest.NewRequest(http.MethodPost, "/api/transcribe", bytes.NewBuffer(nil))
	req.Header.Set("Content-Type", "audio/webm")
	rr := httptest.NewRecorder()
	srv.handleTranscribe(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty body status = %d, want 400", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/transcribe", bytes.NewBuffer(make([]byte, maxTranscribeBytes+1)))
	req.Header.Set("Content-Type", "audio/webm")
	rr = httptest.NewRecorder()
	srv.handleTranscribe(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", rr.Code)
	}
}

func TestTranscribeEnvKeyOverridesConfig(t *testing.T) {
	clearOpenAIEnv(t)
	t.Setenv("PODIOM_OPENAI_API_KEY", "sk-env")
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	srv.core.SetVoice(config.Voice{OpenAIAPIKey: "sk-config"})

	var gotAuth string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"ok"}`))
	}))
	defer fake.Close()
	srv.transcribeBaseURL = fake.URL

	req := httptest.NewRequest(http.MethodPost, "/api/transcribe", bytes.NewBufferString("audio"))
	req.Header.Set("Content-Type", "audio/webm")
	rr := httptest.NewRecorder()
	srv.handleTranscribe(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}
	if gotAuth != "Bearer sk-env" {
		t.Fatalf("authorization = %q, want env key to win", gotAuth)
	}
}
