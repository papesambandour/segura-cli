package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strings"

	"segura-cli/internal/sftpclient"
)

// handleSFTPList returns directory listing as JSON.
// GET /api/sftp/list?path=/&username=X&ip=Y
func (s *Server) handleSFTPList(w http.ResponseWriter, r *http.Request) {
	credential := r.URL.Query().Get("username")
	device := r.URL.Query().Get("ip")
	dirPath := r.URL.Query().Get("path")

	if credential == "" || device == "" {
		http.Error(w, `{"error":"username and ip required"}`, http.StatusBadRequest)
		return
	}
	if dirPath == "" {
		dirPath = "/"
	}

	sess, err := s.sftpMgr.GetSession(credential, device)
	if err != nil {
		log.Printf("SFTP session error for %s@%s: %v", credential, device, err)
		http.Error(w, fmt.Sprintf(`{"error":"SFTP connection failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	entries, err := sess.ListDir(dirPath)
	if err != nil {
		log.Printf("SFTP list error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":"list failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"path":    dirPath,
		"entries": entries,
	})
}

// handleSFTPRead returns file content as text.
// GET /api/sftp/read?path=/file&username=X&ip=Y
func (s *Server) handleSFTPRead(w http.ResponseWriter, r *http.Request) {
	credential := r.URL.Query().Get("username")
	device := r.URL.Query().Get("ip")
	filePath := r.URL.Query().Get("path")

	if credential == "" || device == "" || filePath == "" {
		http.Error(w, `{"error":"username, ip and path required"}`, http.StatusBadRequest)
		return
	}

	sess, err := s.sftpMgr.GetSession(credential, device)
	if err != nil {
		log.Printf("SFTP session error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":"SFTP connection failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	data, err := sess.ReadFile(filePath, 10*1024*1024)
	if err != nil {
		log.Printf("SFTP read error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":"read failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	info, _ := sess.Stat(filePath)
	size := int64(len(data))
	if info != nil {
		size = info.Size()
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-File-Size", fmt.Sprintf("%d", size))
	w.Write(data)
}

// handleSFTPDownload streams a file as a download.
// GET /api/sftp/download?path=/file&username=X&ip=Y
func (s *Server) handleSFTPDownload(w http.ResponseWriter, r *http.Request) {
	credential := r.URL.Query().Get("username")
	device := r.URL.Query().Get("ip")
	filePath := r.URL.Query().Get("path")

	if credential == "" || device == "" || filePath == "" {
		http.Error(w, `{"error":"username, ip and path required"}`, http.StatusBadRequest)
		return
	}

	sess, err := s.sftpMgr.GetSession(credential, device)
	if err != nil {
		log.Printf("SFTP session error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":"SFTP connection failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	filename := path.Base(filePath)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Type", "application/octet-stream")

	n, info, err := sess.Download(filePath, w)
	if err != nil {
		log.Printf("SFTP download error: %v", err)
		// If we haven't written anything yet, we can send an error
		if n == 0 {
			http.Error(w, fmt.Sprintf(`{"error":"download failed: %s"}`, err.Error()), http.StatusInternalServerError)
		}
		return
	}
	if info != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	}
	log.Printf("SFTP download: %s (%d bytes)", filePath, n)
}

// handleSFTPUpload handles file upload.
// POST /api/sftp/upload?path=/dir&username=X&ip=Y[&sudo=true]
func (s *Server) handleSFTPUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	credential := r.URL.Query().Get("username")
	device := r.URL.Query().Get("ip")
	dirPath := r.URL.Query().Get("path")
	useSudo := r.URL.Query().Get("sudo") == "true"

	if credential == "" || device == "" || dirPath == "" {
		http.Error(w, `{"error":"username, ip and path required"}`, http.StatusBadRequest)
		return
	}

	sess, err := s.sftpMgr.GetSession(credential, device)
	if err != nil {
		log.Printf("SFTP session error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":"SFTP connection failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Parse multipart form (max 100MB)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"parse form failed: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	var results []map[string]interface{}
	hasPermError := false
	files := r.MultipartForm.File["files"]
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			results = append(results, map[string]interface{}{
				"name": fh.Filename, "error": err.Error(),
			})
			continue
		}

		// Build remote path, preserving relative paths from folder upload
		remoteName := fh.Filename
		remotePath := dirPath
		if !strings.HasSuffix(remotePath, "/") {
			remotePath += "/"
		}
		remotePath += remoteName

		var n int64
		var uploadErr error
		if useSudo {
			n, uploadErr = sess.UploadSudo(remotePath, f, s.cfg.Password)
		} else {
			n, uploadErr = sess.Upload(remotePath, f)
		}
		f.Close()

		if uploadErr != nil {
			isPerm := sftpclient.IsPermissionError(uploadErr)
			if isPerm {
				hasPermError = true
			}
			results = append(results, map[string]interface{}{
				"name":            remoteName,
				"error":           uploadErr.Error(),
				"permissionError": isPerm,
			})
		} else {
			results = append(results, map[string]interface{}{
				"name": remoteName, "size": n, "status": "ok", "sudo": useSudo,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results":         results,
		"permissionError": hasPermError,
	})
}

// handleSFTPWrite saves content to a remote file (for editing).
// PUT /api/sftp/write?path=/file&username=X&ip=Y[&sudo=true]
func (s *Server) handleSFTPWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, `{"error":"PUT required"}`, http.StatusMethodNotAllowed)
		return
	}

	credential := r.URL.Query().Get("username")
	device := r.URL.Query().Get("ip")
	filePath := r.URL.Query().Get("path")
	useSudo := r.URL.Query().Get("sudo") == "true"

	if credential == "" || device == "" || filePath == "" {
		http.Error(w, `{"error":"username, ip and path required"}`, http.StatusBadRequest)
		return
	}

	sess, err := s.sftpMgr.GetSession(credential, device)
	if err != nil {
		log.Printf("SFTP session error: %v", err)
		http.Error(w, fmt.Sprintf(`{"error":"SFTP connection failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"read body failed: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	if useSudo {
		// Direct sudo write — use the vault password for sudo
		if err := sess.WriteFileSudo(filePath, data, s.cfg.Password); err != nil {
			log.Printf("SFTP sudo write error: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": fmt.Sprintf("sudo write failed: %s", err.Error()),
			})
			return
		}
		log.Printf("SFTP sudo write: %s (%d bytes)", filePath, len(data))
	} else {
		// Normal write — detect permission errors
		if err := sess.WriteFile(filePath, data); err != nil {
			log.Printf("SFTP write error: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":           fmt.Sprintf("write failed: %s", err.Error()),
				"permissionError": sftpclient.IsPermissionError(err),
			})
			return
		}
		log.Printf("SFTP write: %s (%d bytes)", filePath, len(data))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"path":   filePath,
		"size":   len(data),
		"sudo":   useSudo,
	})
}
