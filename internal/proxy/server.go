// Package proxy 本地 HTTP 服务器：入口协议解析、鉴权、渠道路由与 SSE 转发。
package proxy

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"

	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/router"
)

// MaxBodyBytes 请求体上限（图片 base64 场景预留）。
const MaxBodyBytes = 32 << 20

type Server struct {
	store    *config.Store
	registry *provider.Registry
	router   *router.Router
	mux      *http.ServeMux
	handler  *Handler

	mu     sync.RWMutex
	server *http.Server
}

func NewServer(store *config.Store, registry *provider.Registry) *Server {
	rt := router.New(store, registry)
	s := &Server{store: store, registry: registry, router: rt}
	s.handler = &Handler{store: store, registry: registry, router: rt}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /v1/models", s.handler.ListModels)
	s.mux.HandleFunc("POST /v1/messages", s.handler.Messages)
	s.mux.HandleFunc("POST /v1/chat/completions", s.handler.ChatCompletions)
	return s
}

// NewDefaultServer 读取默认路径配置并构造服务器（CLI 骨架用）。
func NewDefaultServer() (*Server, error) {
	path, err := config.DefaultPath("config.json")
	if err != nil {
		return nil, err
	}
	store := config.NewStore(path)
	if _, err := store.Load(); err != nil {
		return nil, err
	}
	return NewServer(store, provider.NewRegistry()), nil
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) ListenAndServe() error {
	cfg := s.store.Get()
	s.mu.Lock()
	s.server = &http.Server{Addr: cfg.Address(), Handler: s.mux}
	s.mu.Unlock()
	return s.server.ListenAndServe()
}

func (s *Server) Close() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// authorize 校验本地代理密钥（Bearer 或 x-api-key）。
func (s *Server) authorize(r *http.Request) bool {
	key := strings.TrimSpace(s.store.Get().LocalKey)
	if key == "" {
		return false
	}
	got := r.Header.Get("Authorization")
	if strings.HasPrefix(got, "Bearer ") {
		got = strings.TrimSpace(strings.TrimPrefix(got, "Bearer "))
	} else {
		got = strings.TrimSpace(r.Header.Get("x-api-key"))
	}
	got = strings.TrimSpace(got)
	return len(got) == len(key) && subtle.ConstantTimeCompare([]byte(got), []byte(key)) == 1
}
