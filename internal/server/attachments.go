package server

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
)

const (
	maxAttachmentOriginalBytes = 10 << 20
	maxAttachmentVisualBytes   = 10 << 20
	maxAttachmentRequestBytes  = 22 << 20
	maxAttachmentDimension     = 2000
)

var supportedAttachmentMIMEs = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

func (s *Server) handleSessionAttachments(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if sessionID == "" {
		http.Error(w, "session id is required", http.StatusBadRequest)
		return
	}
	if _, err := s.core.GetSession(r.Context(), sessionID); err != nil {
		writeJSON(w, nil, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentRequestBytes)
	if err := r.ParseMultipartForm(maxAttachmentRequestBytes); err != nil {
		http.Error(w, "attachment upload is too large", http.StatusRequestEntityTooLarge)
		return
	}
	defer r.MultipartForm.RemoveAll()
	original, originalHeader, err := readMultipartFile(r, "file", maxAttachmentOriginalBytes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	visual, _, err := readMultipartFile(r, "visual", maxAttachmentVisualBytes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mimeType := http.DetectContentType(original)
	if !supportedAttachmentMIMEs[mimeType] {
		http.Error(w, "unsupported photo format; use JPEG, PNG, GIF, or WebP", http.StatusUnsupportedMediaType)
		return
	}
	if got := http.DetectContentType(visual); got != "image/jpeg" {
		http.Error(w, "normalized visual must be a JPEG", http.StatusUnsupportedMediaType)
		return
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(visual))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		http.Error(w, "normalized visual is not a valid JPEG", http.StatusBadRequest)
		return
	}
	if config.Width > maxAttachmentDimension || config.Height > maxAttachmentDimension {
		http.Error(w, "normalized visual dimensions exceed 2000 px", http.StatusBadRequest)
		return
	}
	attachment, err := s.core.CreateAttachment(r.Context(), core.CreateAttachmentInput{
		SessionID: sessionID,
		Name:      originalHeader.Filename,
		MIMEType:  mimeType,
		Original:  original,
		Visual:    visual,
		Width:     config.Width,
		Height:    config.Height,
	})
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, attachment, nil)
}

func readMultipartFile(r *http.Request, field string, limit int64) ([]byte, *multipartFileHeader, error) {
	file, header, err := r.FormFile(field)
	if err != nil {
		return nil, nil, fmt.Errorf("%s is required", field)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", field, err)
	}
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("%s is empty", field)
	}
	if int64(len(data)) > limit {
		return nil, nil, fmt.Errorf("%s exceeds the size limit", field)
	}
	return data, &multipartFileHeader{Filename: header.Filename}, nil
}

type multipartFileHeader struct{ Filename string }

func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/attachments/")
	id, sub, _ := strings.Cut(rest, "/")
	if id == "" || (sub != "" && sub != "thumbnail") {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		attachment, data, err := s.core.ReadAttachment(r.Context(), id, sub == "thumbnail")
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, nil, err)
			return
		}
		contentType := attachment.MIMEType
		filename := attachment.Name
		if sub == "thumbnail" {
			contentType = "image/jpeg"
			filename = "preview.jpg"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": filename}))
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data)
	case http.MethodDelete:
		if sub != "" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.core.DeleteDraftAttachment(r.Context(), id); err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, map[string]string{"deleted": id}, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
