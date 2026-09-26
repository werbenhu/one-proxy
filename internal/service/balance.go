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

type BalanceMetric struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	Percent *int   `json:"percent,omitempty"`
	ResetAt string `json:"resetAt,omitempty"`
}

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
		return BalanceView{}, fmt.Errorf("提供商 %s 不存在", id)
	}
	if p.BalanceKind == "" && p.BalanceURL == "" {
		return BalanceView{Supported: false, Summary: "该提供商没有可用的余额接口"}, nil
	}
	if p.Type == config.TypeGrok {
		return s.grokBalance(p)
	}
	if p.BalanceKind == "grok" {
		return BalanceView{}, fmt.Errorf("Grok 订阅额度仅适用于 Grok 类型的提供商")
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
		return BalanceView{}, fmt.Errorf("查询余额: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return BalanceView{}, fmt.Errorf("读取余额响应: %w", err)
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
			return BalanceView{}, fmt.Errorf("余额接口返回 HTTP %d", resp.StatusCode)
		}
		return BalanceView{}, fmt.Errorf("余额接口返回 HTTP %d: %s", resp.StatusCode, message)
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
		return BalanceView{}, fmt.Errorf("提供商 %s 未注册或未启用", p.ID)
	}
	source, ok := adapter.(grokTokenSource)
	if !ok {
		return BalanceView{}, fmt.Errorf("Grok 提供商未处于网页授权模式")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	token, err := source.AccessToken(ctx)
	if err != nil {
		return BalanceView{}, fmt.Errorf("获取 Grok 授权：%w", err)
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
		return BalanceView{}, fmt.Errorf("查询 Grok 额度: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return BalanceView{}, fmt.Errorf("读取 Grok 额度响应: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return BalanceView{}, fmt.Errorf("Grok 额度接口返回 HTTP %d", resp.StatusCode)
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
		return BalanceView{}, fmt.Errorf("解析 Grok 额度响应: %w", err)
	}
	period := bill.Config.CurrentPeriod
	if period == nil {
		return BalanceView{}, fmt.Errorf("Grok 额度接口返回成功，但响应格式无法识别")
	}
	percent := 0
	if bill.Config.CreditUsagePercent != nil {
		percent = int(*bill.Config.CreditUsagePercent + 0.5)
	}
	metric := BalanceMetric{Label: grokPeriodLabel(period.Type), Value: fmt.Sprintf("已用 %d%%", percent), Percent: &percent}
	if period.End != "" {
		if t, err := time.Parse(time.RFC3339, period.End); err == nil {
			metric.ResetAt = t.Format(time.RFC3339)
		}
	}
	return BalanceView{
		Supported: true, Kind: "grok", Summary: metric.Value,
		Details: []BalanceMetric{metric}, CheckedAt: time.Now().Format(time.RFC3339),
	}, nil
}

func grokPeriodLabel(periodType string) string {
	switch strings.ToUpper(strings.TrimPrefix(periodType, "USAGE_PERIOD_TYPE_")) {
	case "WEEKLY":
		return "每周额度"
	case "MONTHLY":
		return "每月额度"
	case "DAILY":
		return "每日额度"
	default:
		return "订阅额度"
	}
}

func balanceEndpoint(kind, custom, base string) (string, error) {
	if strings.TrimSpace(custom) != "" {
		if _, err := url.ParseRequestURI(custom); err != nil {
			return "", fmt.Errorf("余额接口地址无效: %w", err)
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
			return "", fmt.Errorf("无法从 Base URL 推导 Kimi 余额接口")
		}
		return u.Scheme + "://" + u.Host + "/v1/users/me/balance", nil
	default:
		return "", fmt.Errorf("未知余额类型 %q，请配置自定义余额接口", kind)
	}
}

func parseBalance(kind string, body []byte) (BalanceView, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return BalanceView{}, fmt.Errorf("解析余额响应: %w", err)
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
				view.Details = append(view.Details, BalanceMetric{Label: currency + " 可用余额", Value: total})
			}
			appendMetric(&view, currency+" 充值余额", info["topped_up_balance"])
			appendMetric(&view, currency+" 赠送余额", info["granted_balance"])
		}
	case "openrouter":
		data, _ := root["data"].(map[string]any)
		total, used := numberValue(data["total_credits"]), numberValue(data["total_usage"])
		if total != nil && used != nil {
			view.Summary = "$" + strconv.FormatFloat(*total-*used, 'f', 4, 64)
			view.Details = append(view.Details, BalanceMetric{Label: "总额度", Value: "$" + formatNumber(*total)}, BalanceMetric{Label: "已使用", Value: "$" + formatNumber(*used)})
		}
	case "moonshot":
		data, _ := root["data"].(map[string]any)
		for _, key := range []string{"available_balance", "cash_balance", "voucher_balance"} {
			appendMetric(&view, balanceLabel(key), data[key])
		}
	case "zai-coding", "minimax-coding":
		data, _ := root["data"].(map[string]any)
		appendPlanMetrics(&view, data)
	case "kimi-coding":
		startLen := len(view.Details)
		if dataList, ok := root["data"].([]any); ok {
			for _, raw := range dataList {
				item, _ := raw.(map[string]any)
				label := firstText(item, "name", "title")
				if model, _ := item["model_name"].(string); model == "all" {
					label = "每周额度"
				} else if label == "" {
					label = model
				}
				if label == "" {
					label = "限额"
				}
				appendWindowMetric(&view, label, item)
			}
		} else {
			if usage, ok := root["usage"].(map[string]any); ok {
				appendWindowMetric(&view, "每周额度", usage)
			}
			limits, _ := root["limits"].([]any)
			for _, raw := range limits {
				item, _ := raw.(map[string]any)
				label := "窗口限额"
				if window, ok := item["window"].(map[string]any); ok {
					label = windowLabel(window)
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
				appendWindowMetric(&view, label, detail)
			}
		}
		// 5 小时等短窗口排在每周额度之上
		sort.SliceStable(view.Details[startLen:], func(i, j int) bool {
			return view.Details[startLen+i].Label != "每周额度" && view.Details[startLen+j].Label == "每周额度"
		})
	case "commandcode":
		credits, _ := root["credits"].(map[string]any)
		windows, _ := root["windowLimits"].(map[string]any)
		for _, pair := range []struct{ key, label string }{{"monthlyCredits", "月度额度"}, {"purchasedCredits", "购买额度"}, {"freeCredits", "免费额度"}} {
			appendMetric(&view, pair.label, credits[pair.key])
		}
		for _, pair := range []struct{ key, label string }{{"fiveHour", "5 小时"}, {"weekly", "每周"}} {
			window, _ := windows[pair.key].(map[string]any)
			used, cap := textValue(window["used"]), textValue(window["cap"])
			if used != "" && cap != "" {
				view.Details = append(view.Details, BalanceMetric{Label: pair.label + "已用 / 上限", Value: used + " / " + cap, ResetAt: resetTimeValue(window)})
			}
		}
	default:
		appendPlanMetrics(&view, root)
	}
	if view.Summary == "" && len(view.Details) > 0 {
		view.Summary = view.Details[0].Value
	}
	if len(view.Details) == 0 {
		return BalanceView{}, fmt.Errorf("余额接口返回成功，但响应格式无法识别")
	}
	return view, nil
}

func appendPlanMetrics(view *BalanceView, data map[string]any) {
	for _, listKey := range []string{"limits", "remains", "packages"} {
		items, ok := data[listKey].([]any)
		if !ok {
			continue
		}
		for i, raw := range items {
			item, _ := raw.(map[string]any)
			label := firstText(item, "name", "type", "model", "resource_name")
			if label == "" {
				label = fmt.Sprintf("套餐 %d", i+1)
			}
			label = planLabel(label)
			value := firstText(item, "remaining", "remain", "available", "left", "currentValue", "usage", "value")
			limit := firstText(item, "limit", "total", "max", "usageLimit")
			if value != "" && limit != "" {
				value += " / " + limit
			}
			metric := BalanceMetric{Label: label, Value: value, ResetAt: resetTimeValue(item)}
			if pct := numberValue(item["percentage"]); pct != nil {
				p := int(*pct + 0.5)
				metric.Percent = &p
				if metric.Value == "" {
					metric.Value = fmt.Sprintf("已用 %d%%", p)
				}
			}
			if itemType, _ := item["type"].(string); itemType == "TIME_LIMIT" && metric.Value != "" && !strings.HasPrefix(metric.Value, "已用") {
				metric.Value = "已用 " + metric.Value + " 次"
			}
			if metric.Value == "" {
				continue
			}
			view.Details = append(view.Details, metric)
		}
	}
	for _, key := range []string{"available_balance", "balance", "remaining", "remain", "total"} {
		appendMetric(view, balanceLabel(key), data[key])
	}
}

func appendMetric(view *BalanceView, label string, value any) {
	if text := textValue(value); text != "" {
		view.Details = append(view.Details, BalanceMetric{Label: label, Value: text})
	}
}

// appendWindowMetric 解析 Kimi Coding 套餐的用量窗口：used / limit（数值常为字符串，
// 也可能是 used_amount / limit_amount），used 缺失时用 limit - remaining 推算。
func appendWindowMetric(view *BalanceView, label string, item map[string]any) {
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
		Label:   label,
		Value:   formatNumber(*used) + " / " + formatNumber(*limit),
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

// windowLabel 把 Kimi 的 window（duration + timeUnit）映射为可读标签。
func windowLabel(window map[string]any) string {
	duration := numberValue(window["duration"])
	unit, _ := window["timeUnit"].(string)
	if unit == "" {
		unit, _ = window["time_unit"].(string)
	}
	if duration == nil {
		return "窗口限额"
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
		return "5 小时窗口"
	case 10080:
		return "每周额度"
	default:
		if minutes >= 1440 && int(minutes)%1440 == 0 {
			return fmt.Sprintf("%d 天窗口", int(minutes)/1440)
		}
		return fmt.Sprintf("%d 分钟窗口", int(minutes))
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

func planLabel(key string) string {
	labels := map[string]string{"TIME_LIMIT": "5 小时限额", "TOKENS_LIMIT": "Token 限额"}
	if label := labels[key]; label != "" {
		return label
	}
	return key
}

func balanceLabel(key string) string {
	labels := map[string]string{"available_balance": "可用余额", "cash_balance": "现金余额", "voucher_balance": "赠送余额", "balance": "余额", "remaining": "剩余额度", "remain": "剩余额度", "total": "总额度"}
	if label := labels[key]; label != "" {
		return label
	}
	return key
}
