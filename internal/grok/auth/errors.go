package auth

import "errors"

var (
	ErrAuthorizationPending    = errors.New("waiting for user to complete Grok authorization")
	ErrSlowDown                = errors.New("Grok authorization polling too fast")
	ErrAuthorizationDenied     = errors.New("Grok authorization was denied or expired")
	ErrCredentialMissing       = errors.New("Grok credentials are not configured")
	ErrReauthorizationRequired = errors.New("Grok authorization is invalid; re-authorization required")
)
