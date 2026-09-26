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
	if view.Details[0].LabelKey != "balance.available" || len(view.Details[0].LabelArgs) != 1 || view.Details[0].LabelArgs[0] != "CNY" {
		t.Fatalf("unexpected label: %+v", view.Details[0])
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
	if view.Details[0].LabelKey != "quota.total" || view.Details[1].LabelKey != "quota.usedAmount" {
		t.Fatalf("unexpected labels: %+v", view.Details)
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
	if view.Details[0].LabelKey != "plan.tokensLimit" || view.Details[0].ValueKey != "usedOverLimit" {
		t.Fatalf("unexpected metric: %+v", view.Details[0])
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
	if view.Details[0].LabelKey != "plan.timeLimit" || view.Details[0].ValueKey != "usedPercent" || view.Details[0].ValueArgs[0] != "0" || view.Details[0].Percent == nil || *view.Details[0].Percent != 0 {
		t.Fatalf("unexpected time limit metric: %+v", view.Details[0])
	}
	if view.Details[1].LabelKey != "plan.tokensLimit" || view.Details[1].Value != "337430" || view.Details[1].Percent == nil || *view.Details[1].Percent != 3 {
		t.Fatalf("unexpected token limit metric: %+v", view.Details[1])
	}
}

func TestParseCodingPlanTimeLimitTimes(t *testing.T) {
	view, err := parseBalance("zai-coding", []byte(`{"data":{"limits":[{"type":"TIME_LIMIT","currentValue":973,"percentage":9}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	m := view.Details[0]
	if m.LabelKey != "plan.timeLimit" || m.ValueKey != "usedTimes" || m.ValueArgs[0] != "973" || m.Percent == nil || *m.Percent != 9 {
		t.Fatalf("unexpected time limit metric: %+v", m)
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
	if fiveHour.LabelKey != "window.5h" || fiveHour.Percent == nil || *fiveHour.Percent != 2 {
		t.Fatalf("unexpected five-hour metric: %+v", fiveHour)
	}
	weekly := view.Details[1]
	if weekly.LabelKey != "period.weekly" || weekly.ValueKey != "usedOverLimit" || weekly.ValueArgs[0] != "120000" || weekly.ValueArgs[1] != "500000" || weekly.Percent == nil || *weekly.Percent != 24 {
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
	if fiveHour.LabelKey != "window.5h" || fiveHour.ValueKey != "usedOverLimit" || fiveHour.ValueArgs[0] != "1000" || fiveHour.ValueArgs[1] != "50000" || fiveHour.Percent == nil || *fiveHour.Percent != 2 {
		t.Fatalf("unexpected five-hour metric: %+v", fiveHour)
	}
	if view.Details[1].LabelKey != "period.weekly" || view.Details[1].ValueArgs[0] != "120000" {
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
	if view.Details[0].LabelKey != "credits.monthly" {
		t.Fatalf("unexpected credits metric: %+v", view.Details[0])
	}
	fiveHour := view.Details[3]
	if fiveHour.LabelKey != "window.5h" || fiveHour.ValueKey != "usedOverLimit" || fiveHour.ValueArgs[0] != "1.5" || fiveHour.ValueArgs[1] != "14" {
		t.Fatalf("unexpected five-hour metric: %+v", fiveHour)
	}
}
