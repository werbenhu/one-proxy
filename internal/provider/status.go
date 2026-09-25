package provider

import (
	"sync"
	"time"
)

// ChannelState 渠道运行状态（内存态，重启清零——个人工具刻意不做持久化）。
type ChannelState struct {
	Status       string    // ok | cooling | auth-failed
	CoolingUntil time.Time // cooling 状态的恢复时间点
	FailReason   string    // 进入非 ok 状态的原因（UI 展示）
	UpdatedAt    time.Time
}

const (
	StatusOK         = "ok"
	StatusCooling    = "cooling"
	StatusAuthFailed = "auth-failed"
)

// Available 判断渠道当前是否可进候选。
func (s ChannelState) Available(now time.Time) bool {
	switch s.Status {
	case StatusOK:
		return true
	case StatusCooling:
		return now.After(s.CoolingUntil)
	case StatusAuthFailed:
		return false
	}
	return true
}

// EffectiveStatus 计算 UI 展示状态（cooling 过期视为 ok）。
func (s ChannelState) EffectiveStatus(now time.Time) string {
	if s.Status == StatusCooling && now.After(s.CoolingUntil) {
		return StatusOK
	}
	return s.Status
}

// Registry 保存渠道适配器实例与状态（内存态，读写锁保护）。
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
	states   map[string]ChannelState
	now      func() time.Time
}

func NewRegistry() *Registry {
	return &Registry{
		adapters: map[string]Adapter{},
		states:   map[string]ChannelState{},
		now:      time.Now,
	}
}

func (r *Registry) Register(id string, a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[id] = a
	if _, ok := r.states[id]; !ok {
		r.states[id] = ChannelState{Status: StatusOK, UpdatedAt: r.now()}
	}
}

// Reset replaces all runtime adapters after configuration changes.
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters = map[string]Adapter{}
	r.states = map[string]ChannelState{}
}

func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.adapters, id)
	delete(r.states, id)
}

func (r *Registry) Get(id string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[id]
	return a, ok
}

func (r *Registry) SetCooling(id string, until time.Time, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[id] = ChannelState{Status: StatusCooling, CoolingUntil: until, FailReason: reason, UpdatedAt: r.now()}
}

func (r *Registry) SetAuthFailed(id string, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[id] = ChannelState{Status: StatusAuthFailed, FailReason: reason, UpdatedAt: r.now()}
}

func (r *Registry) SetOK(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[id] = ChannelState{Status: StatusOK, UpdatedAt: r.now()}
}

func (r *Registry) State(id string) ChannelState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.states[id]
	if !ok {
		return ChannelState{Status: StatusOK, UpdatedAt: r.now()}
	}
	return s
}

// Available 判断渠道状态是否可用（含 cooling 过期自动恢复）。
func (r *Registry) Available(id string) bool {
	return r.State(id).Available(r.now())
}

// Snapshot 返回全部渠道的展示状态（cooling 过期归一为 ok）。
func (r *Registry) Snapshot() map[string]ChannelState {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]ChannelState, len(r.states))
	now := r.now()
	for id, s := range r.states {
		out[id] = ChannelState{
			Status: s.EffectiveStatus(now), CoolingUntil: s.CoolingUntil,
			FailReason: s.FailReason, UpdatedAt: s.UpdatedAt,
		}
	}
	return out
}
