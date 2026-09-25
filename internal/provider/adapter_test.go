package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
)

func TestBadRuestMaySwitch(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"error":{"message":"model xyz not found"}}`, true},
		{`{"error":{"message":"max_tokens exceeds limit"}}`, true},
		{`{"error":{"message":"invalid schema for tool"}}`, false},
		{"", false},
	}
	for _, c := range cases {
		e := &UpstreamError{Kind: ErrKindBadRequest, StatusCode: 400, Body: c.body}
		if got := e.BadRequestMaySwitch(); got != c.want {
			t.Errorf("body=%q want=%v got=%v", c.body, c.want, got)
		}
	}
}

func TestMaySwitchMatrix(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&UpstreamError{Kind: ErrKindQuota, StatusCode: 429}, true},
		{&UpstreamError{Kind: ErrKindRateLimit, StatusCode: 429}, true},
		{&UpstreamError{Kind: ErrKindAuth, StatusCode: 401}, true},
		{&UpstreamError{Kind: ErrKindUpstream, StatusCode: 502}, true},
		{&UpstreamError{Kind: ErrKindBadRequest, StatusCode: 400, Body: "bad schema"}, false},
		{&UpstreamError{Kind: ErrKindBadRequest, StatusCode: 400, Body: "model not found"}, true},
		{errors.New("connection refused"), true},
	}
	for i, c := range cases {
		if got := MaySwitch(c.err); got != c.want {
			t.Errorf("case %d: want=%v got=%v (%v)", i, c.want, got, c.err)
		}
	}
}

func TestKindOf(t *testing.T) {
	if KindOf(&UpstreamError{Kind: ErrKindAuth}) != ErrKindAuth {
		t.Fatal("UpstreamError kind 提取失败")
	}
	if KindOf(errors.New("x")) != ErrKindUpstream {
		t.Fatal("普通 error 应归 Upstream")
	}
}

func TestChannelStateAvailable(t *testing.T) {
	now := time.Now()
	cases := []struct {
		state ChannelState
		want  bool
	}{
		{ChannelState{Status: StatusOK}, true},
		{ChannelState{Status: StatusCooling, CoolingUntil: now.Add(time.Minute)}, false},
		{ChannelState{Status: StatusCooling, CoolingUntil: now.Add(-time.Second)}, true},
		{ChannelState{Status: StatusAuthFailed}, false},
	}
	for i, c := range cases {
		if got := c.state.Available(now); got != c.want {
			t.Errorf("case %d: want=%v got=%v", i, c.want, got)
		}
	}
}

type fakeAdapter struct{}

func (fakeAdapter) Invoke(ctx context.Context, req *anthropic.Request) (*anthropic.Response, error) {
	return nil, nil
}
func (fakeAdapter) Stream(ctx context.Context, req *anthropic.Request) (<-chan anthropic.Event, error) {
	return nil, nil
}
func (fakeAdapter) Models(ctx context.Context) ([]ModelInfo, error) { return nil, nil }
func (fakeAdapter) NormalizeError(err error) ErrorKind              { return ErrKindNone }

func TestRegistryStates(t *testing.T) {
	r := NewRegistry()
	r.Register("ch-a", fakeAdapter{})
	if !r.Available("ch-a") {
		t.Fatal("注册后应为可用")
	}
	r.SetCooling("ch-a", time.Now().Add(time.Hour), "429")
	if r.Available("ch-a") {
		t.Fatal("冷却中不应可用")
	}
	r.SetCooling("ch-a", time.Now().Add(-time.Minute), "429")
	if !r.Available("ch-a") {
		t.Fatal("冷却过期应自动恢复")
	}
	r.SetAuthFailed("ch-b", "401")
	if r.Available("ch-b") {
		t.Fatal("auth-failed 不应可用")
	}
	snap := r.Snapshot()
	if snap["ch-b"].Status != StatusAuthFailed {
		t.Fatalf("快照状态错误: %+v", snap["ch-b"])
	}
	if snap["ch-a"].Status != StatusOK {
		t.Fatalf("冷却过期快照应为 ok: %+v", snap["ch-a"])
	}
	if _, ok := r.Get("ch-a"); !ok {
		t.Fatal("Get 失败")
	}
	r.Unregister("ch-a")
	if _, ok := r.Get("ch-a"); ok {
		t.Fatal("Unregister 失败")
	}
	if fmt.Sprint(State{Status: StatusOK}.Status) == "" {
		t.Fatal("unreachable")
	}
}

type State = ChannelState
