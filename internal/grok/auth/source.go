package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type TokenRefresher interface {
	Refresh(context.Context, string) (Token, error)
}

// OAuth 凭据（与 one-proxy 渠道 Extra JSON 对齐）。
type OAuth struct {
	AccessToken  string    `json:"accessToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
}

type TokenStore interface {
	OAuth() OAuth
	SaveOAuth(OAuth) error
	InvalidateOAuth() error
}

type Source struct {
	refresher TokenRefresher
	store     TokenStore
	refreshMu sync.Mutex
	now       func() time.Time
}

func NewSource(refresher TokenRefresher, store TokenStore) *Source {
	return &Source{refresher: refresher, store: store, now: time.Now}
}

func (s *Source) AccessToken(ctx context.Context) (string, error) {
	current := s.store.OAuth()
	if s.isFresh(current) {
		return current.AccessToken, nil
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	current = s.store.OAuth()
	if s.isFresh(current) {
		return current.AccessToken, nil
	}
	if strings.TrimSpace(current.RefreshToken) == "" {
		return "", ErrCredentialMissing
	}
	token, err := s.refresher.Refresh(ctx, current.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrReauthorizationRequired) {
			if invalidateErr := s.store.InvalidateOAuth(); invalidateErr != nil {
				return "", errors.Join(ErrReauthorizationRequired, fmt.Errorf("清除失效 Grok 授权: %w", invalidateErr))
			}
			return "", ErrReauthorizationRequired
		}
		return "", fmt.Errorf("刷新 Grok 授权: %w", err)
	}
	updated := OAuth{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: token.ExpiresAt}
	if updated.RefreshToken == "" {
		updated.RefreshToken = current.RefreshToken
	}
	if err := s.store.SaveOAuth(updated); err != nil {
		return "", fmt.Errorf("保存 Grok 授权: %w", err)
	}
	return updated.AccessToken, nil
}

func (s *Source) isFresh(value OAuth) bool {
	return strings.TrimSpace(value.AccessToken) != "" && value.ExpiresAt.After(s.now().Add(5*time.Minute))
}
