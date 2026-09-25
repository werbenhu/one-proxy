// Package provider 定义适配器接口与渠道状态。本包不 import 任何适配器实现
// （依赖倒置）：适配器单向依赖此接口，proxy/router 只面向接口编程。
package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
)

// ErrorKind 归一化错误类别，决定路由切换行为（plan.md §5.3）。
type ErrorKind int

const (
	ErrKindNone ErrorKind = iota
	ErrKindQuota
	ErrKindRateLimit
	ErrKindAuth
	ErrKindUpstream
	ErrKindBadRequest
)

// UpstreamError 携带上游状态码与诊断片段；Body 供 400 细分判断
// （含 model/max_tokens 字样允许切换一次）。
type UpstreamError struct {
	Kind       ErrorKind
	StatusCode int
	Status     string
	Body       string
}

func (e *UpstreamError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("上游返回 HTTP %d: %s", e.StatusCode, e.Body)
	}
	return fmt.Sprintf("上游返回 HTTP %d", e.StatusCode)
}

// BadRequestMaySwitch 判断 400 错误是否允许切换渠道（plan.md：模型不存在/
// max_tokens 超限可能是渠道差异，schema 校验错误不该切）。
func (e *UpstreamError) BadRequestMaySwitch() bool {
	if e.Kind != ErrKindBadRequest {
		return false
	}
	for _, kw := range []string{"model", "max_tokens"} {
		for i := 0; i+len(kw) <= len(e.Body); i++ {
			if e.Body[i:i+len(kw)] == kw {
				return true
			}
		}
	}
	return false
}

// KindOf 从 error 提取 ErrorKind；非 UpstreamError 归为 Upstream（网络等）。
func KindOf(err error) ErrorKind {
	var ue *UpstreamError
	if errors.As(err, &ue) {
		return ue.Kind
	}
	return ErrKindUpstream
}

// MaySwitch 判断错误是否应切换到下一候选渠道。
func MaySwitch(err error) bool {
	switch KindOf(err) {
	case ErrKindQuota, ErrKindRateLimit, ErrKindAuth, ErrKindUpstream:
		return true
	case ErrKindBadRequest:
		var ue *UpstreamError
		if errors.As(err, &ue) {
			return ue.BadRequestMaySwitch()
		}
		return false
	}
	return false
}

type ModelInfo struct {
	ID string `json:"id"`
}

// Adapter 实例对应一个渠道。Invoke/Stream 接收 canonical 请求；
// Stream 的 retryReader 参数用于故障切换重试时重读请求体（channel 只能读一次）。
type Adapter interface {
	Invoke(ctx context.Context, req *anthropic.Request) (*anthropic.Response, error)
	Stream(ctx context.Context, req *anthropic.Request) (<-chan anthropic.Event, error)
	Models(ctx context.Context) ([]ModelInfo, error)
	NormalizeError(err error) ErrorKind
}

// RawForwardCapable 可选能力：适配器声明支持的入口协议一跳直通
// （grok：chat/responses/messages → Responses，避免双重转换）。
type RawForwardCapable interface {
	SupportedProtocols() []Protocol
	ForwardRaw(ctx context.Context, protocol Protocol, body []byte, header http.Header, stream bool) (*RawResult, error)
}

type Protocol string

const (
	ProtocolAnthropic Protocol = "anthropic"
	ProtocolChat      Protocol = "chat"
	ProtocolResponses Protocol = "responses"
)

// RawResult 直通结果：非流式给完整 body；流式给事件/字节流（由 proxy 层
// 按入口协议原样回写，tee 旁路提取 usage）。
type RawResult struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

// OAuthCapable 可选能力（仅 grok）：设备授权流程。
// token 刷新由适配器内部托管（过期前缓冲 + 单飞 + invalid_grant 置 auth-failed）。
type OAuthCapable interface {
	StartDeviceAuth(ctx context.Context) (DeviceAuthInfo, error)
	PollDeviceAuth(ctx context.Context, deviceCode string) error
}

type DeviceAuthInfo struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete,omitempty"`
	ExpiresInSeconds        int    `json:"expiresInSeconds"`
	IntervalSeconds         int    `json:"intervalSeconds"`
}
