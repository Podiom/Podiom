package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/gateway"
	"github.com/Podiom/Podiom/internal/store"
)

func tinyJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 30, G: 90, B: 180, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func attachmentUploadRequest(t *testing.T, sessionID, filename string, original, visual []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filePart, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := filePart.Write(original); err != nil {
		t.Fatal(err)
	}
	visualPart, err := writer.CreateFormFile("visual", "visual.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := visualPart.Write(visual); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/attachments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestPhotoAttachmentUploadRetrieveAndDeleteDraft(t *testing.T) {
	ctx := context.Background()
	paths, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	session, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: "atlas", Origin: store.OriginWeb})
	if err != nil {
		t.Fatal(err)
	}
	original := tinyPNG(t)
	visual := tinyJPEG(t, 2, 2)

	upload := httptest.NewRecorder()
	srv.handleSession(upload, attachmentUploadRequest(t, session.ID, "../holiday.png", original, visual))
	if upload.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", upload.Code, upload.Body.String())
	}
	var attachment store.Attachment
	if err := json.Unmarshal(upload.Body.Bytes(), &attachment); err != nil {
		t.Fatal(err)
	}
	if attachment.ID == "" || attachment.Name != "holiday.png" || attachment.MIMEType != "image/png" || attachment.Width != 2 || attachment.Height != 2 {
		t.Fatalf("attachment metadata = %+v", attachment)
	}
	keeper, _, err := gateway.LoadOrCreate(paths.GatewayToken)
	if err != nil {
		t.Fatal(err)
	}
	authedServer := New(Options{Core: srv.core, Paths: paths, Tokens: keeper})
	unauthenticated := serve(authedServer, httptest.NewRequest(http.MethodGet, "/api/attachments/"+attachment.ID, nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated attachment status=%d", unauthenticated.Code)
	}
	authenticatedRequest := httptest.NewRequest(http.MethodGet, "/api/attachments/"+attachment.ID, nil)
	authenticatedRequest.Header.Set(gateway.Header, keeper.Current())
	if authenticated := serve(authedServer, authenticatedRequest); authenticated.Code != http.StatusOK {
		t.Fatalf("authenticated attachment status=%d body=%s", authenticated.Code, authenticated.Body.String())
	}

	get := httptest.NewRecorder()
	srv.handleAttachment(get, httptest.NewRequest(http.MethodGet, "/api/attachments/"+attachment.ID, nil))
	if get.Code != http.StatusOK || !bytes.Equal(get.Body.Bytes(), original) || get.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("original response status=%d headers=%v", get.Code, get.Header())
	}
	thumbnail := httptest.NewRecorder()
	srv.handleAttachment(thumbnail, httptest.NewRequest(http.MethodGet, "/api/attachments/"+attachment.ID+"/thumbnail", nil))
	if thumbnail.Code != http.StatusOK || !bytes.Equal(thumbnail.Body.Bytes(), visual) || thumbnail.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumbnail response status=%d headers=%v", thumbnail.Code, thumbnail.Header())
	}
	deleted := httptest.NewRecorder()
	srv.handleAttachment(deleted, httptest.NewRequest(http.MethodDelete, "/api/attachments/"+attachment.ID, nil))
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete draft status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	missing := httptest.NewRecorder()
	srv.handleAttachment(missing, httptest.NewRequest(http.MethodGet, "/api/attachments/"+attachment.ID, nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("get deleted attachment status=%d", missing.Code)
	}
}

func TestPhotoAttachmentRejectsSpoofedOriginalAndInvalidVisual(t *testing.T) {
	ctx := context.Background()
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatal(err)
	}
	session, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: "atlas", Origin: store.OriginWeb})
	if err != nil {
		t.Fatal(err)
	}

	spoofed := httptest.NewRecorder()
	srv.handleSession(spoofed, attachmentUploadRequest(t, session.ID, "fake.png", []byte("not an image"), tinyJPEG(t, 2, 2)))
	if spoofed.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("spoofed original status=%d body=%s", spoofed.Code, spoofed.Body.String())
	}
	invalidVisual := httptest.NewRecorder()
	srv.handleSession(invalidVisual, attachmentUploadRequest(t, session.ID, "valid.png", tinyPNG(t), []byte("not a jpeg")))
	if invalidVisual.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("invalid visual status=%d body=%s", invalidVisual.Code, invalidVisual.Body.String())
	}
	tooWide := httptest.NewRecorder()
	srv.handleSession(tooWide, attachmentUploadRequest(t, session.ID, "wide.png", tinyPNG(t), tinyJPEG(t, maxAttachmentDimension+1, 1)))
	if tooWide.Code != http.StatusBadRequest {
		t.Fatalf("oversized dimensions status=%d body=%s", tooWide.Code, tooWide.Body.String())
	}
	empty := httptest.NewRecorder()
	srv.handleSession(empty, attachmentUploadRequest(t, session.ID, "empty.png", nil, tinyJPEG(t, 2, 2)))
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("empty original status=%d body=%s", empty.Code, empty.Body.String())
	}
	oversizedBytes := make([]byte, maxAttachmentOriginalBytes+1)
	copy(oversizedBytes, tinyPNG(t))
	oversized := httptest.NewRecorder()
	srv.handleSession(oversized, attachmentUploadRequest(t, session.ID, "large.png", oversizedBytes, tinyJPEG(t, 2, 2)))
	if oversized.Code != http.StatusBadRequest {
		t.Fatalf("oversized original status=%d body=%s", oversized.Code, oversized.Body.String())
	}
}
