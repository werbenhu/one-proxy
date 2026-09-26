package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/provider"
)

// BalanceMetric 的 Label/Value 字段已不再使用：文案由前端按 LabelKey+LabelArgs、
// ValueKey+ValueArgs 语义字段本地化渲染。未识别的原始标签放在 LabelRaw，
// 前端在缺少语义 key 时回退显示。
type BalanceMetric struct {
	Label     string   `json:"label,omitempty"`
	LabelKey  string   `json:"labelKey,omitempty"`
	LabelArgs []string `json:"labelArgs,omitempty"`
	Value     string   `json:"value,omitempty"`
	ValueKey  string   `json:"valueKey,omitempty"`
	ValueArgs []string `json:"valueArgs,omitempty"`
	Percent   *int     `json:"percent,omitempty"`
	ResetAt   string   `json:"resetAt,omitempty"`
}

// LabelRaw 兼容字段：未识别的标签原样返回给前端显示。
const LabelRaw = "raw"

type BalanceView struct {
	Supported bool            `json:"supported"`
	Kind      string          `json:"kind"`
	Summary   string          `json:"summary"`
	Details   []BalanceMetric `json:"details"`
	CheckedAt string          `json:"checkedAt"`
}

func (s *Service) ProviderBalance(id string) (BalanceView, error) {
	p, ok := s.store.Get().Provider(id)
	if !ok {
		return BalanceView{}, fmt.Errorf("provider %s does not exist", id)
	}
	if p.BalanceKind == "" && p.BalanceURL == "" {
		return BalanceView{Supported: false, Summary: "该提供商没有可用的余额接口"}, nil
	}
	if p.Type == config.TypeGrok {
		return s.grokBalance(p)
	}
	if p.BalanceKind == "grok" {
		return BalanceView{}, fmt.Errorf("Grok subscription quota only applies to Grok-type providers")
	}
	endpoint, err := balanceEndpoint(p.BalanceKind, p.BalanceURL, p.BaseURL)
	if err != nil {
		return BalanceView{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return BalanceView{}, err
	}
	key := p.BalanceKey
	if key == "" {
		key = p.APIKey
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	resp, err := provider.Client(p.EffectiveProxyURL(s.store.Get().GlobalProxy)).Do(req)
	if err != nil {
		return BalanceView{}, fmt.Errorf("query balance: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return BalanceView{}, fmt.Errorf("read balance response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if strings.Contains(message, "<") {
			message = ""
		}
		if len(message) > 300 {
			message = message[:300] + "..."
		}
		if message == "" {
			return BalanceView{}, fmt.Errorf("balance endpoint returned HTTP %d", resp.StatusCode)
		}
		return BalanceView{}, fmt.Errorf("balance endpoint returned HTTP %d: %s", resp.StatusCode, message)
	}
	view, err := parseBalance(p.BalanceKind, body)
	if err != nil {
		return BalanceView{}, err
	}
	view.Supported, view.Kind, view.CheckedAt = true, p.BalanceKind, time.Now().Format(time.RFC3339)
	return view, nil
}

// grokTokenSource Grok 适配器暴露 OAuth token 的接口（*grokadapter.Adapter 实现）。
type grokTokenSource interface {
	AccessToken(ctx context.Context) (string, error)
}

// grokBalance 查询 Grok 订阅用量：GET {cli-chat-proxy}/billing?format=credits，
// 数据与 grok-cli 的 /usage 视图同源（creditUsagePercent + currentPeriod）。
func (s *Service) grokBalance(p config.ProviderAccount) (BalanceView, error) {
	var extra struct {
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(p.Extra, &extra)
	if extra.Mode == "api_key" {
		return BalanceView{Supported: false, Summary: "API Key 模式暂无额度查询接口"}, nil
	}
	adapter, ok := s.registry.Get(p.ID)
	if !ok {
		return BalanceView{}, fmt.Errorf("provider %s is not registered or not enabled", p.ID)
	}
	source, ok := adapter.(grokTokenSource)
	if !ok {
		return BalanceView{}, fmt.Errorf("Grok provider is not in web authorization mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	token, err := source.AccessToken(ctx)
	if err != nil {
		return BalanceView{}, fmt.Errorf("get Grok authorization: %w", err)
	}
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://cli-chat-proxy.grok.com/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/billing?format=credits", nil)
	if err != nil {
		return BalanceView{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := provider.Client(p.EffectiveProxyURL(s.store.Get().GlobalProxy)).Do(req)
	if err != nil {
		return BalanceView{}, fmt.Errorf("query Grok quota: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return BalanceView{}, fmt.Errorf("read Grok quota response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return BalanceView{}, fmt.Errorf("Grok quota endpoint returned HTTP %d", resp.StatusCode)
	}
	var bill struct {
		Config struct {
			CreditUsagePercent *float64 `json:"creditUsagePercent"`
			CurrentPeriod      *struct {
				Type string `json:"type"`
				End  string `json:"end"`
			} `json:"currentPeriod"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &bill); err != nil {
		return BalanceView{}, fmt.Errorf("parse Grok quota response: %w", err)
	}
	period := bill.Config.CurrentPeriod
	if period == nil {
		return BalanceView{}, fmt.Errorf("Grok quota endpoint returned success but the response format is unrecognized")
	}
	percent := 0
	if bill.Config.CreditUsagePercent != nil {
		percent = int(*bill.Config.CreditUsagePercent + 0.5)
	}
	pct := strconv.Itoa(percent)
	metric := BalanceMetric{LabelKey: grokPeriodLabel(period.Type), ValueKey: "usedPercent", ValueArgs: []string{pct}, Percent: &percent}
	if period.End != "" {
		if t, err := time.Parse(time.RFC3339, period.End); err == nil {
			metric.ResetAt = t.Format(time.RFC3339)
		}
	}
	return BalanceView{
		Supported: true, Kind: "grok", Summary: pct + "%",
		Details: []BalanceMetric{metric}, CheckedAt: time.Now().Format(time.RFC3339),
	}, nil
}

func grokPeriodLabel(periodType string) string {
	switch strings.ToUpper(strings.TrimPrefix(periodType, "USAGE_PERIOD_TYPE_")) {
	case "WEEKLY":
		return "period.weekly"
	case "MONTHLY":
		return "period.monthly"
	case "DAILY":
		return "period.daily"
	default:
		return "period.subscription"
	}
}

func balanceEndpoint(kind, custom, base string) (string, error) {
	if strings.TrimSpace(custom) != "" {
		if _, err := url.ParseRequestURI(custom); err != nil {
			return "", fmt.Errorf("invalid balance endpoint URL: %w", err)
		}
		return strings.TrimRight(custom, "/"), nil
	}
	switch kind {
	case "deepseek":
		return "https://api.deepseek.com/user/balance", nil
	case "commandcode":
		return "https://api.commandcode.ai/alpha/billing/credits", nil
	case "openrouter":
		return "https://openrouter.ai/api/v1/credits", nil
	case "zai-coding":
		return "https://api.z.ai/api/monitor/usage/quota/limit", nil
	case "minimax-coding":
		return "https://api.minimax.io/v1/api/openplatform/coding_plan/remains", nil
	case "kimi-coding":
		return "https://api.kimi.com/coding/v1/usages", nil
	case "moonshot":
		u, err := url.Parse(base)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "", fmt.Errorf("cannot derive Kimi balance endpoint from Base URL")
		}
		return u.Scheme + "://" + u.Host + "/v1/users/me/balance", nil
	default:
		return "", fmt.Errorf("unknown balance kind %q; please configure a custom balance endpoint", kind)
	}
}

func parseBalance(kind string, body []byte) (BalanceView, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return BalanceView{}, fmt.Errorf("parse balance response: %w", err)
	}
	view := BalanceView{Details: []BalanceMetric{}}
	switch kind {
	case "deepseek":
		infos, _ := root["balance_infos"].([]any)
		for _, raw := range infos {
			info, _ := raw.(map[string]any)
			currency := textValue(info["currency"])
			total := textValue(info["total_balance"])
			if total != "" {
				view.Details = append(view.Details, BalanceMetric{LabelKey: "balance.available", LabelArgs: []string{currency}, Value: total})
			}
			appendMetric(&view, "balance.toppedUp", []string{currency}, info["topped_up_balance"])
			appendMetric(&view, "balance.granted", []string{currency}, info["granted_balance"])
		}
	case "openrouter":
		data, _ := root["data"].(map[string]any)
		total, used := numberValue(data["total_credits"]), numberValue(data["total_usage"])
		if total != nil && used != nil {
			view.Summary = "$" + strconv.FormatFloat(*total-*used, 'f', 4, 64)
			view.Details = append(view.Details,
				BalanceMetric{LabelKey: "quota.total", Value: "$" + formatNumber(*total)},
				BalanceMetric{LabelKey: "quota.usedAmount", Value: "$" + formatNumber(*used)})
		}
	case "moonshot":
		data, _ := root["data"].(map[string]any)
		for _, key := range []string{"available_balance", "cash_balance", "voucher_balance"} {
			appendMetric(&view, balanceLabel(key), nil, data[key])
		}
	case "zai-coding", "minimax-coding":
		data, _ := root["data"].(map[string]any)
		appendPlanMetrics(&view, data)
	case "kimi-coding":
		startLen := len(view.Details)
		if dataList, ok := root["data"].([]any); ok {
			for _, raw := range dataList {
				item, _ := raw.(map[string]any)
				labelKey, labelArgs := LabelRaw, []string{}
				label := firstText(item, "name", "title")
				if model, _ := item["model_name"].(string); model == "all" {
					labelKey = "period.weekly"
				} else if label == "" {
					label, _ = item["model_name"].(string)
				}
				if label == "" && labelKey == LabelRaw {
					label = "限额"
				}
				if label != "" {
					labelArgs = []string{label}
				}
				appendWindowMetric(&view, labelKey, labelArgs, item)
			}
		} else {
			if usage, ok := root["usage"].(map[string]any); ok {
				appendWindowMetric(&view, "period.weekly", nil, usage)
			}
			limits, _ := root["limits"].([]any)
			for _, raw := range limits {
				item, _ := raw.(map[string]any)
				labelKey, labelArgs := LabelRaw, []string{"窗口限额"}
				if window, ok := item["window"].(map[string]any); ok {
					labelKey, labelArgs = windowLabel(window)
				}
				detail := item
				if d, ok := item["detail"].(map[string]any); ok {
					detail = d
					for _, key := range []string{"resetTime", "reset_at", "reset_in"} {
						if detail[key] == nil {
							detail[key] = item[key]
						}
					}
				}
				appendWindowMetric(&view, labelKey, labelArgs, detail)
			}
		}
		// 5 小时等短窗口排在每周额度之上
		sort.SliceStable(view.Details[startLen:], func(i, j int) bool {
			return view.Details[startLen+i].LabelKey != "period.weekly" && view.Details[startLen+j].LabelKey == "period.weekly"
		})
	case "commandcode":
		credits, _ := root["credits"].(map[string]any)
		windows, _ := root["windowLimits"].(map[string]any)
		for _, pair := range []struct{ key, label string }{{"monthlyCredits", "credits.monthly"}, {"purchasedCredits", "credits.purchased"}, {"freeCredits", "credits.free"}} {
			appendMetric(&view, pair.label, nil, credits[pair.key])
		}
		for _, pair := range []struct{ key, label string }{{"fiveHour", "window.5h"}, {"weekly", "window.weekly"}} {
			window, _ := windows[pair.key].(map[string]any)
			used, cap := textValue(window["used"]), textValue(window["cap"])
			if used != "" && cap != "" {
				view.Details = append(view.Details, BalanceMetric{
					LabelKey: pair.label,
					ValueKey: "usedOverLimit", ValueArgs: []string{used, cap},
					ResetAt: resetTimeValue(window),
				})
			}
		}
	default:
		appendPlanMetrics(&view, root)
	}
	if view.Summary == "" && len(view.Details) > 0 {
		view.Summary = summaryText(view.Details[0])
	}
	if len(view.Details) == 0 {
		return BalanceView{}, fmt.Errorf("balance endpoint returned success but the response format is unrecognized")
	}
	return view, nil
}

// summaryText 为 summary 给一个语言中立的短文本（前端只在无 percent 明细时展示）。
func summaryText(m BalanceMetric) string {
	switch m.ValueKey {
	case "usedPercent":
		return m.ValueArgs[0] + "%"
	case "usedOverLimit":
		return m.ValueArgs[0] + " / " + m.ValueArgs[1]
	case "usedTimes":
		return m.ValueArgs[0]
	}
	return m.Value
}

func appendPlanMetrics(view *BalanceView, data map[string]any) {
	for _, listKey := range []string{"limits", "remains", "packages"} {
		items, ok := data[listKey].([]any)
		if !ok {
			continue
		}
		for i, raw := range items {
			item, _ := raw.(map[string]any)
			labelKey, labelArgs := planLabel(firstText(item, "name", "type", "model", "resource_name"))
			if labelKey == LabelRaw && len(labelArgs) == 0 {
				labelArgs = []string{fmt.Sprintf("套餐 %d", i+1)}
			}
			value := firstText(item, "remaining", "remain", "available", "left", "currentValue", "usage", "value")
			limit := firstText(item, "limit", "total", "max", "usageLimit")
			metric := BalanceMetric{LabelKey: labelKey, LabelArgs: labelArgs, ResetAt: resetTimeValue(item)}
			if value != "" && limit != "" {
				metric.ValueKey = "usedOverLimit"
				metric.ValueArgs = []string{value, limit}
			} else {
				metric.Value = value
			}
			if pct := numberValue(item["percentage"]); pct != nil {
				p := int(*pct + 0.5)
				metric.Percent = &p
				if metric.Value == "" && metric.ValueKey == "" {
					metric.ValueKey = "usedPercent"
					metric.ValueArgs = []string{strconv.Itoa(p)}
				}
			}
			if itemType, _ := item["type"].(string); itemType == "TIME_LIMIT" && metric.Value != "" {
				metric.ValueKey = "usedTimes"
				metric.ValueArgs = []string{metric.Value}
				metric.Value = ""
			}
			if metric.Value == "" && metric.ValueKey == "" {
				continue
			}
			view.Details = append(view.Details, metric)
		}
	}
	for _, key := range []string{"available_balance", "balance", "remaining", "remain", "total"} {
		appendMetric(view, balanceLabel(key), nil, data[key])
	}
}

func appendMetric(view *BalanceView, labelKey string, labelArgs []string, value any) {
	if text := textValue(value); text != "" {
		view.Details = append(view.Details, BalanceMetric{LabelKey: labelKey, LabelArgs: labelArgs, Value: text})
	}
}

// appendWindowMetric 解析 Kimi Coding 套餐的用量窗口：used / limit（数值常为字符串，
// 也可能是 used_amount / limit_amount），used 缺失时用 limit - remaining 推算。
func appendWindowMetric(view *BalanceView, labelKey string, labelArgs []string, item map[string]any) {
	limit := numberValue(item["limit"])
	if limit == nil {
		limit = numberValue(item["limit_amount"])
	}
	used := numberValue(item["used"])
	if used == nil {
		used = numberValue(item["used_amount"])
	}
	if used == nil && limit != nil {
		if remaining := numberValue(item["remaining"]); remaining != nil {
			value := *limit - *remaining
			used = &value
		}
	}
	if used == nil || limit == nil || *limit <= 0 {
		return
	}
	percent := int(*used / *limit * 100)
	view.Details = append(view.Details, BalanceMetric{
		LabelKey: labelKey, LabelArgs: labelArgs,
		ValueKey: "usedOverLimit", ValueArgs: []string{formatNumber(*used), formatNumber(*limit)},
		Percent: &percent,
		ResetAt: resetTimeValue(item),
	})
}

// resetTimeValue 提取窗口重置时间并统一为 RFC3339：兼容 ISO 字符串（resetTime /
// reset_at / nextResetTime）、epoch 秒或毫秒，以及 reset_in 秒数倒计时。
func resetTimeValue(item map[string]any) string {
	for _, key := range []string{"resetTime", "reset_at", "reset_time", "nextResetTime", "resetAt"} {
		switch v := item[key].(type) {
		case string:
			if parsed, err := time.Parse(time.RFC3339, v); err == nil {
				return parsed.Format(time.RFC3339)
			}
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return epochTime(n)
			}
		case float64:
			return epochTime(int64(v))
		}
	}
	if resetIn := numberValue(item["reset_in"]); resetIn != nil {
		return time.Now().Add(time.Duration(*resetIn) * time.Second).Format(time.RFC3339)
	}
	return ""
}

func epochTime(value int64) string {
	if value <= 0 {
		return ""
	}
	if value > 1e12 {
		return time.UnixMilli(value).Format(time.RFC3339)
	}
	return time.Unix(value, 0).Format(time.RFC3339)
}

// windowLabel 把 Kimi 的 window（duration + timeUnit）映射为语义标签。
func windowLabel(window map[string]any) (string, []string) {
	duration := numberValue(window["duration"])
	unit, _ := window["timeUnit"].(string)
	if unit == "" {
		unit, _ = window["time_unit"].(string)
	}
	if duration == nil {
		return "window.generic", nil
	}
	minutes := *duration
	switch {
	case strings.Contains(unit, "HOUR"):
		minutes *= 60
	case strings.Contains(unit, "DAY"):
		minutes *= 1440
	}
	switch int(minutes) {
	case 300:
		return "window.5h", nil
	case 10080:
		return "window.weekly", nil
	default:
		if minutes >= 1440 && int(minutes)%1440 == 0 {
			return "window.days", []string{strconv.Itoa(int(minutes) / 1440)}
		}
		return "window.minutes", []string{strconv.Itoa(int(minutes))}
	}
}

func firstText(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := textValue(m[key]); value != "" {
			return value
		}
	}
	return ""
}

func textValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return formatNumber(v)
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func numberValue(value any) *float64 {
	var n float64
	switch v := value.(type) {
	case float64:
		n = v
	case string:
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil
		}
		n = parsed
	default:
		return nil
	}
	return &n
}

func formatNumber(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

// planLabel 识别已知的套餐限额类型；未识别的原文标签以 raw 形式返回（空则不携带参数）。
func planLabel(key string) (string, []string) {
	labels := map[string]string{"TIME_LIMIT": "plan.timeLimit", "TOKENS_LIMIT": "plan.tokensLimit"}
	if label := labels[key]; label != "" {
		return label, nil
	}
	if key == "" {
		return LabelRaw, nil
	}
	return LabelRaw, []string{key}
}

func balanceLabel(key string) string {
	labels := map[string]string{
		"available_balance": "balance.available",
		"cash_balance":      "balance.cash",
		"voucher_balance":   "balance.voucher",
		"balance":           "balance.generic",
		"remaining":         "balance.remaining",
		"remain":            "balance.remaining",
		"total":             "quota.total",
	}
	if label := labels[key]; label != "" {
		return label
	}
	return LabelRaw
}
