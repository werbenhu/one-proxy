package service

import "testing"

func TestParseDeepSeekBalance(t *testing.T) {
	view, err := parseBalance("deepseek", []byte(`{
		"is_available": true,
		"balance_infos": [{"currency":"CNY","total_balance":"12.50","granted_balance":"2.50","topped_up_balance":"10.00"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if view.Summary != "12.50" || len(view.Details) != 3 {
		t.Fatalf("unexpected balance: %+v", view)
	}
}

func TestParseOpenRouterBalance(t *testing.T) {
	view, err := parseBalance("openrouter", []byte(`{"data":{"total_credits":20,"total_usage":3.25}}`))
	if err != nil {
		t.Fatal(err)
	}
	if view.Summary != "$16.7500" || len(view.Details) != 2 {
		t.Fatalf("unexpected credits: %+v", view)
	}
}

func TestParseCodingPlan(t *testing.T) {
	view, err := parseBalance("zai-coding", []byte(`{"data":{"limits":[{"type":"TOKENS_LIMIT","remaining":1200,"limit":5000}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if view.Summary != "1200 / 5000" {
		t.Fatalf("unexpected plan quota: %+v", view)
	}
}

func TestParseCodingPlanPercentage(t *testing.T) {
	view, err := parseBalance("zai-coding", []byte(`{"data":{"limits":[{"type":"TIME_LIMIT","unit":5,"number":1,"percentage":0},{"type":"TOKENS_LIMIT","unit":3,"number":1,"usage":337430,"percentage":3}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Details) != 2 {
		t.Fatalf("unexpected details: %+v", view)
	}
	if view.Details[0].Label != "5 小时限额" || view.Details[0].Value != "已用 0%" || view.Details[0].Percent == nil || *view.Details[0].Percent != 0 {
		t.Fatalf("unexpected time limit metric: %+v", view.Details[0])
	}
	if view.Details[1].Label != "Token 限额" || view.Details[1].Value != "337430" || view.Details[1].Percent == nil || *view.Details[1].Percent != 3 {
		t.Fatalf("unexpected token limit metric: %+v", view.Details[1])
	}
}

func TestParseKimiCodingUsage(t *testing.T) {
	view, err := parseBalance("kimi-coding", []byte(`{"usage":{"used":"120000","limit":"500000"},"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"used":"1000","limit":"50000"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Details) != 2 {
		t.Fatalf("unexpected details: %+v", view)
	}
	fiveHour := view.Details[0]
	if fiveHour.Label != "5 小时窗口" || fiveHour.Percent == nil || *fiveHour.Percent != 2 {
		t.Fatalf("unexpected five-hour metric: %+v", fiveHour)
	}
	weekly := view.Details[1]
	if weekly.Label != "每周额度" || weekly.Value != "120000 / 500000" || weekly.Percent == nil || *weekly.Percent != 24 {
		t.Fatalf("unexpected weekly metric: %+v", weekly)
	}
}

func TestParseKimiCodingUsageDetailWrapped(t *testing.T) {
	view, err := parseBalance("kimi-coding", []byte(`{"usage":{"used_amount":"120000","limit_amount":"500000"},"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"used":"1000","limit":"50000"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Details) != 2 {
		t.Fatalf("unexpected details: %+v", view)
	}
	fiveHour := view.Details[0]
	if fiveHour.Label != "5 小时窗口" || fiveHour.Value != "1000 / 50000" || fiveHour.Percent == nil || *fiveHour.Percent != 2 {
		t.Fatalf("unexpected five-hour metric: %+v", fiveHour)
	}
	if view.Details[1].Label != "每周额度" || view.Details[1].Value != "120000 / 500000" {
		t.Fatalf("unexpected weekly metric: %+v", view.Details[1])
	}
}

func TestParseCommandCodeCredits(t *testing.T) {
	view, err := parseBalance("commandcode", []byte(`{"credits":{"monthlyCredits":69.5,"purchasedCredits":0,"freeCredits":0},"windowLimits":{"limited":true,"fiveHour":{"used":1.5,"cap":14},"weekly":{"used":1.5,"cap":35}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if view.Summary != "69.5" || len(view.Details) != 5 {
		t.Fatalf("Command Code 额度解析错误: %+v", view)
	}
}
