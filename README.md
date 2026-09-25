# OneProxy

本地多供应商 LLM 中转代理 — Golang + Wails 桌面应用（个人工具）。

把手里的供应商（订阅 coding plan + 按 token 计费 API）合并成一套本地 API：多个同类套餐自动互备，用量统一记账。

## 能力

- **三种入口**：Anthropic `/v1/messages`（主力，Claude Code 用）、OpenAI Chat `/v1/chat/completions`、OpenAI Responses `/v1/responses`（grok 直通）；`GET /v1/models`
- **供应商**：z.ai / bigmodel / kimi / minimax / deepseek / openrouter / opencode zen / commandcode / grok（OAuth 设备授权 + 自动刷新），以及任意自定义 OpenAI/Anthropic 兼容端点
- **渠道路由**（One API 模型）：提供商=类型，渠道=线路；优先级主备 + 粘性；429 冷却切换（指数退避）、401/403 标记鉴权失败、400 按 body 细分；全不可用返回聚合人话错误
- **模型别名**：`ModelMapping` 对外名 → 上游模型名（如 `claude-sonnet-4-6 → kimi-k3`）
- **协议保真**：canonical=Anthropic；请求内容字段不深度解析原样传输；未知 block/字段保留；`anthropic-beta`/`anthropic-version` 头白名单透传
- **用量统计**：SQLite 按渠道×模型（对外+上游双维度），今天/7天/30天/全部
- **托盘常驻**：关窗口不退出

按个人工具裁剪（不做）：配额前置预判（只信 429）、权重随机、分组多租户、图表、多密钥、余额查询。

## 快速开始

### 桌面模式

```
wails build
build\bin\OneProxy.exe
```

界面里「添加渠道」选预设（如 Kimi Coding Plan）→ 填 API Key → 测试 → 启用。

### CLI 模式（调试）

```
OneProxy.exe cli
```

读 `%AppData%\OneProxy\config.json` 并启动代理，不启 GUI。

### 手配 config.json 示例

```json
{
  "listenHost": "127.0.0.1",
  "listenPort": 8280,
  "localKey": "自动生成",
  "retainDays": 90,
  "channels": [
    {
      "id": "ch-kimi-a",
      "name": "Kimi 套餐 A",
      "type": "anthropic-compat",
      "baseUrl": "https://api.moonshot.cn/anthropic",
      "apiKey": "sk-xxx",
      "models": ["claude-sonnet-4-6"],
      "modelMapping": { "claude-sonnet-4-6": "kimi-k3" },
      "priority": 10,
      "enabled": true
    },
    {
      "id": "ch-kimi-b",
      "name": "Kimi 套餐 B（备用）",
      "type": "anthropic-compat",
      "baseUrl": "https://api.moonshot.cn/anthropic",
      "apiKey": "sk-yyy",
      "models": ["claude-sonnet-4-6"],
      "modelMapping": { "claude-sonnet-4-6": "kimi-k3" },
      "priority": 5,
      "enabled": true
    }
  ]
}
```

A 用完（429）自动切 B，冷却期满回 A。

### 客户端接入

Claude Code：

```bash
export ANTHROPIC_BASE_URL="http://127.0.0.1:8280"
export ANTHROPIC_API_KEY="<本地密钥>"
```

OpenAI 客户端：

```bash
export OPENAI_BASE_URL="http://127.0.0.1:8280/v1"
export OPENAI_API_KEY="<本地密钥>"
```

调试直连语法：`model=ch-<渠道ID>/<上游模型名>` 绕过路由直发指定渠道（仅 `ch-` 前缀解析，OpenRouter 的 `org/model` 不受影响）。

## 架构

```
proxy(入口/鉴权/SSE) → router(候选/主备/冷却/切换) → provider.Adapter
                                                ├─ anthropiccompat（恒等重序列化）
                                                ├─ openaicompat（convert 双向）
                                                └─ grok（OAuth 刷新 + Responses 直通）
usage(SQLite 记账) ← router 埋点回调          config(JSON)
```

- 新增标准兼容供应商：UI 选「自定义」预设填 BaseURL，零代码
- 新增专有供应商：实现 `provider.Adapter` 接口 + main 里 import

包结构、设计决策与裁剪理由见 [plan.md](plan.md)。

## 开发

```
go test ./...        # 后端测试
wails dev            # 桌面开发模式
wails build          # 打 Windows 包
```
