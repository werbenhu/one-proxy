package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func jsonMarshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

// Store 持久化配置到 JSON 文件；损坏时备份后重置（同 grok-proxy 模式）。
type Store struct {
	path string
	mu   sync.RWMutex
	cfg  Config
}

func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) Load() (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.cfg = Default()
			return s.cfg, nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg, err := parse(data)
	if err != nil {
		return Config{}, err
	}
	s.cfg = Normalize(cfg)
	return cfg, nil
}

func parse(data []byte) (Config, error) {
	var cfg Config
	if err := jsonUnmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.ListenHost == "" && cfg.ListenPort == 0 && strings.TrimSpace(cfg.LocalKey) == "" {
		cfg = Default()
	}
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return Normalize(cfg), nil
}

func (s *Store) Save(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(cfg)
}

func (s *Store) saveLocked(cfg Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}
	cfg = Normalize(cfg)
	data, err := jsonMarshalIndent(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create config dir: %w", err)
		}
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	s.cfg = cfg
	return nil
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Update 在锁内修改配置并持久化。
func (s *Store) Update(fn func(*Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(s.cfg)
	if err != nil {
		return fmt.Errorf("copy config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("copy config: %w", err)
	}
	fn(&cfg)
	return s.saveLocked(cfg)
}

// BackupInvalidAndReset 备份损坏配置并恢复默认值，返回备份路径。
func (s *Store) BackupInvalidAndReset() (string, error) {
	backup := fmt.Sprintf("%s.invalid.%s", s.path, time.Now().Format("20060102-150405"))
	if err := os.Rename(s.path, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("backup corrupt config: %w", err)
	}
	cfg := Default()
	if err := s.Save(cfg); err != nil {
		return "", err
	}
	return backup, nil
}

// DefaultPath 返回平台用户配置目录下的配置文件路径。
func DefaultPath(filename string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("get user config dir: %w", err)
	}
	return filepath.Join(dir, "OneProxy", filename), nil
}
