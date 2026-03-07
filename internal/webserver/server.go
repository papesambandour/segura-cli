package webserver

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"segura-cli/internal/config"
	"segura-cli/internal/sftpclient"
)

//go:embed all:static
var staticFiles embed.FS

// Server is the SEGURA web server.
type Server struct {
	cfg     *config.Config
	sm      *SessionManager
	sftpMgr *sftpclient.Manager
}

// Start launches the web server on the given port.
func Start(cfg *config.Config, port int) error {
	sm := NewSessionManager(cfg)
	sftpMgr := sftpclient.NewManager(cfg)
	srv := &Server{cfg: cfg, sm: sm, sftpMgr: sftpMgr}

	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/credentials", srv.handleCredentials)
	mux.HandleFunc("/api/status", srv.handleStatus)
	mux.HandleFunc("/api/ws", srv.handleWS)

	// SFTP routes
	mux.HandleFunc("/api/sftp/list", srv.handleSFTPList)
	mux.HandleFunc("/api/sftp/read", srv.handleSFTPRead)
	mux.HandleFunc("/api/sftp/download", srv.handleSFTPDownload)
	mux.HandleFunc("/api/sftp/upload", srv.handleSFTPUpload)
	mux.HandleFunc("/api/sftp/write", srv.handleSFTPWrite)

	// Static files
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("failed to setup static files: %w", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	addr := fmt.Sprintf(":%d", port)
	log.Printf("SEGURA Web Server starting on http://localhost%s", addr)
	return http.ListenAndServe(addr, logMiddleware(mux))
}

// logMiddleware logs HTTP requests.
func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
