// Package proxy 本地 HTTP 服务器：入口协议解析、鉴权、渠道路由与 SSE 转发。
package proxy

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/router"
	"github.com/werbenhu/one-proxy/internal/usage"
)

// MaxBodyBytes 请求体上限（图片 base64 场景预留）。
const MaxBodyBytes = 32 << 20

type Server struct {
	store    *config.Store
	registry *provider.Registry
	router   *router.Router
	mux      *http.ServeMux
	handler  *Handler
	recorder *usage.Recorder

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
	s.mux.HandleFunc("POST /v1/responses", s.handler.Responses)
	return s
}

// AttachUsage 接入用量记账（usage db 路径；失败时降级为不记账）。
func (s *Server) AttachUsage(dbPath string, protocol string) error {
	st, err := usage.Open(dbPath)
	if err != nil {
		return err
	}
	s.recorder = usage.NewRecorder(st, 256)
	s.router.SetUsageHook(func(info router.RequestInfo) {
		s.recorder.Record(usage.Record{
			CreatedAt:        time.Now(),
			ChannelID:        info.ChannelID,
			ChannelName:      info.ChannelName,
			ModelRequested:   info.ModelRequested,
			ModelUpstream:    info.ModelUpstream,
			Protocol:         protocol,
			InputTokens:      info.Usage.InputTokens,
			OutputTokens:     info.Usage.OutputTokens,
			CacheReadTokens:  info.Usage.CacheReadInputTokens,
			CacheWriteTokens: info.Usage.CacheCreationInputTokens,
			Status:           info.Status,
			LatencyMs:        info.LatencyMs,
			Error:            info.Error,
		})
	})
	return nil
}

// UsageStore 暴露用量查询（service 层用）；未接入时为 nil。
func (s *Server) UsageStore() *usage.Store {
	if s.recorder == nil {
		return nil
	}
	return s.recorder.Store()
}

// CloseUsage 关闭用量记账（测试与停机时释放 db 句柄）。
func (s *Server) CloseUsage() {
	if s.recorder != nil {
		s.recorder.Close()
		s.recorder = nil
	}
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
