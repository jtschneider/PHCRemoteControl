// Package web serves the PHC project as a small, server-rendered website and a
// capability-oriented JSON/SSE API.
package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/controller"
	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
)

//go:embed templates/*.html static/*
var content embed.FS

const maxJSONBody = 1024

type Backend interface {
	Project() domain.Project
	Device(string) (domain.Device, bool)
	Snapshot() controller.Snapshot
	Capabilities(string) ([]controller.Capability, error)
	Execute(context.Context, string, controller.Action) (controller.CommandResult, error)
	Subscribe(int) (<-chan controller.Event, func(), error)
	Close()
}

type ReloadFunc func(context.Context, Backend) (Backend, error)

type Config struct {
	PublicOrigin      string
	STMAddress        string
	Reload            ReloadFunc
	Logger            *slog.Logger
	HeartbeatInterval time.Duration
}

type Server struct {
	origin     *url.URL
	stmAddress string
	reload     ReloadFunc
	logger     *slog.Logger
	heartbeat  time.Duration
	templates  *template.Template
	assets     map[string]asset

	backendMu sync.RWMutex
	backend   Backend
	reloadMu  sync.Mutex

	notifyMu   sync.Mutex
	nextNotify uint64
	notifiers  map[uint64]chan struct{}
}

type asset struct {
	data        []byte
	contentType string
	etag        string
}

func New(backend Backend, config Config) (*Server, error) {
	if backend == nil {
		return nil, errors.New("web: nil backend")
	}
	origin, err := url.Parse(config.PublicOrigin)
	if err != nil || origin.Scheme != "http" || origin.Host == "" || origin.Path != "" ||
		origin.RawQuery != "" || origin.Fragment != "" || origin.User != nil {
		return nil, fmt.Errorf("web: public origin must be an exact http origin")
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = 15 * time.Second
	}
	if config.HeartbeatInterval < 0 {
		return nil, errors.New("web: heartbeat interval cannot be negative")
	}

	tmpl, err := template.New("base.html").Funcs(template.FuncMap{
		"eqAction": func(a controller.Action, b string) bool { return string(a) == b },
	}).ParseFS(content, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: parsing templates: %w", err)
	}
	assets, err := loadAssets()
	if err != nil {
		return nil, err
	}
	return &Server{
		origin: origin, stmAddress: config.STMAddress, reload: config.Reload,
		logger: config.Logger, heartbeat: config.HeartbeatInterval,
		templates: tmpl, assets: assets, backend: backend,
		notifiers: make(map[uint64]chan struct{}),
	}, nil
}

func loadAssets() (map[string]asset, error) {
	staticFS, err := fs.Sub(content, "static")
	if err != nil {
		return nil, fmt.Errorf("web: opening static assets: %w", err)
	}
	entries, err := fs.ReadDir(staticFS, ".")
	if err != nil {
		return nil, fmt.Errorf("web: reading static assets: %w", err)
	}
	result := make(map[string]asset, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(staticFS, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("web: reading static asset %s: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(data)
		contentType := mime.TypeByExtension(path.Ext(entry.Name()))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		result[entry.Name()] = asset{data: data, contentType: contentType,
			etag: `"` + hex.EncodeToString(sum[:]) + `"`}
	}
	return result, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.page)
	mux.HandleFunc("/floors/", s.page)
	mux.HandleFunc("/settings", s.page)
	mux.HandleFunc("/acknowledgments", s.page)
	mux.HandleFunc("/static/", s.static)
	mux.HandleFunc("/api/v1/status", s.statusAPI)
	mux.HandleFunc("/api/v1/project", s.projectAPI)
	mux.HandleFunc("/api/v1/state", s.stateAPI)
	mux.HandleFunc("/api/v1/events", s.eventsAPI)
	mux.HandleFunc("/api/v1/project/reload", s.reloadAPI)
	mux.HandleFunc("/api/v1/devices/", s.commandAPI)
	return s.security(mux)
}

func (s *Server) current() Backend {
	s.backendMu.RLock()
	defer s.backendMu.RUnlock()
	return s.backend
}

func (s *Server) swap(next Backend) Backend {
	s.backendMu.Lock()
	previous := s.backend
	s.backend = next
	s.backendMu.Unlock()
	return previous
}

// CurrentBackend exposes the live backend so the background project loader can
// pass it to the reload function (which schedules chunk reads through it).
func (s *Server) CurrentBackend() Backend { return s.current() }

// Reload downloads and installs a fresh project, serialized against any other
// reload. Used by both the reload endpoint and the startup background loader.
func (s *Server) Reload(ctx context.Context) error {
	if s.reload == nil {
		return errors.New("web: project reload unavailable")
	}
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	next, err := s.reload(ctx, s.current())
	if err != nil {
		return err
	}
	if next == nil {
		return errors.New("web: reload returned no backend")
	}
	s.install(next)
	return nil
}

// install swaps in a new backend, tells SSE clients to reload, and closes the
// previous one. The caller must hold reloadMu.
func (s *Server) install(next Backend) {
	previous := s.swap(next)
	s.notifyProjectReload()
	if previous != nil {
		previous.Close()
	}
}

func (s *Server) Close() {
	s.backendMu.Lock()
	backend := s.backend
	s.backend = nil
	s.backendMu.Unlock()
	if backend != nil {
		backend.Close()
	}
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !equalHost(r.Host, s.origin.Host) {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func equalHost(got, want string) bool {
	return strings.EqualFold(strings.TrimSuffix(got, "."), strings.TrimSuffix(want, "."))
}

func (s *Server) requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func (s *Server) requireMutation(w http.ResponseWriter, r *http.Request) bool {
	if !s.requireMethod(w, r, http.MethodPost) {
		return false
	}
	if r.Header.Get("Origin") != s.origin.String() {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		http.Error(w, "cross-site request rejected", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodGet) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	item, ok := s.assets[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("ETag", item.etag)
	if r.Header.Get("If-None-Match") == item.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", item.contentType)
	_, _ = w.Write(item.data)
}
