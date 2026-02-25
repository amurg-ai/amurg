package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/amurg-ai/amurg/hub/store"
	"github.com/amurg-ai/amurg/pkg/protocol"
)

// sanitizeFilename removes path separators and unsafe characters from a filename
// for use in Content-Disposition headers.
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "." || name == ".." {
		name = "download"
	}
	return name
}

// handleUploadFile handles POST /api/sessions/{sessionID}/files
// Accepts multipart file upload, saves to hub disk, persists a file message,
// and forwards the file to the runtime via WebSocket.
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	identity := getIdentityFromContext(r.Context())

	// Verify session ownership.
	sess, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil || sess == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if sess.UserID != identity.UserID && identity.Role != "admin" {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}

	// Limit request body size.
	r.Body = http.MaxBytesReader(w, r.Body, s.maxFileBytes+1024) // small overhead for multipart headers

	if err := r.ParseMultipartForm(s.maxFileBytes); err != nil {
		writeError(w, http.StatusBadRequest, "file too large or invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer func() { _ = file.Close() }()

	if header.Size > s.maxFileBytes {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file exceeds maximum size of %d bytes", s.maxFileBytes))
		return
	}

	// Read file content.
	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read file")
		return
	}

	// Determine MIME type.
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = mime.TypeByExtension(filepath.Ext(header.Filename))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	fileID := uuid.New().String()
	fileName := filepath.Base(header.Filename)

	// Save to hub disk: {storage_path}/{session_id}/{file_id}/{filename}
	dir := filepath.Join(s.fileStoragePath, sessionID, fileID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.logger.Warn("failed to create file directory", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save file")
		return
	}
	filePath := filepath.Join(dir, fileName)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		s.logger.Warn("failed to write file", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save file")
		return
	}

	// Persist file message in DB.
	meta := protocol.FileMetadata{
		FileID:   fileID,
		Name:     fileName,
		MimeType: mimeType,
		Size:     int64(len(data)),
	}
	metaJSON, _ := json.Marshal(map[string]any{
		"file_id":   meta.FileID,
		"name":      meta.Name,
		"mime_type": meta.MimeType,
		"size":      meta.Size,
		"direction": "upload",
	})

	seq, err := s.store.AppendMessage(r.Context(), &store.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Seq:       0,
		Direction: "user",
		Channel:   "file",
		Content:   string(metaJSON),
		CreatedAt: time.Now(),
	})
	if err != nil {
		s.logger.Warn("failed to persist file message", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to persist file message")
		return
	}

	// Audit log.
	if err := s.store.LogAuditEvent(r.Context(), &store.AuditEvent{
		ID: uuid.New().String(), OrgID: identity.OrgID, Action: "file.upload",
		UserID: identity.UserID, SessionID: sessionID, EndpointID: sess.EndpointID,
		Detail:    json.RawMessage(fmt.Sprintf(`{"file_id":%q,"name":%q,"size":%d}`, fileID, fileName, len(data))),
		CreatedAt: time.Now(),
	}); err != nil {
		s.logger.Warn("failed to log audit event", "action", "file.upload", "error", err)
	}

	// Send file to runtime via WebSocket (base64 encoded).
	s.router.SendFileToRuntime(sess.RuntimeID, sessionID, protocol.FileUpload{
		SessionID: sessionID,
		Metadata:  meta,
		Data:      base64.StdEncoding.EncodeToString(data),
	})

	// Broadcast file message to subscribed UI clients as agent.output with channel="file".
	s.router.BroadcastFileMessage(sessionID, seq, string(metaJSON))

	writeJSON(w, http.StatusOK, map[string]any{
		"file_id":   fileID,
		"name":      fileName,
		"mime_type": mimeType,
		"size":      len(data),
		"seq":       seq,
	})
}

// handleDownloadFile handles GET /api/files/{fileID}?session_id={sessionID}
// Serves a file with Content-Disposition for download.
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	fileID := chi.URLParam(r, "fileID")
	sessionID := r.URL.Query().Get("session_id")

	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id query parameter is required")
		return
	}

	identity := getIdentityFromContext(r.Context())

	// Verify session ownership.
	sess, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil || sess == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if sess.UserID != identity.UserID && identity.Role != "admin" {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}

	// Find file on disk.
	dir := filepath.Join(s.fileStoragePath, sessionID, fileID)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}

	fileName := entries[0].Name()
	filePath := filepath.Join(dir, fileName)

	// Determine MIME type.
	mimeType := mime.TypeByExtension(filepath.Ext(fileName))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Reject symlinks to prevent path traversal.
	fi, err := os.Lstat(filePath)
	if err != nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		writeError(w, http.StatusForbidden, "access denied")
		return
	}

	safeName := sanitizeFilename(fileName)
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, safeName, url.PathEscape(safeName)))
	http.ServeFile(w, r, filePath)
}
