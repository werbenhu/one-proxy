# OneProxy

本地多供应商 LLM 中转代理 — Golang + Wails 桌面应用（个人工具）。

把手里的供应商（订阅 coding plan + 按 token 计费 API）合并成一套本地 API：多个同类套餐自动互备，用量统一记账。

## 能力

- **三种入口**：Anthropic `/v1/messages`（主力，Claude Code 用）、OpenAI Chat `/v1/chat/completions`、OpenAI Responses `/v1/responses`（支持 Grok 和兼容上游直通）；`GET /v1/models`
- **提供商账户**：独立管理 z.ai / bigmodel / kimi / minimax / deepseek / openrouter / opencode zen / cmdc-proxy / Ollama Cloud / Grok（OAuth）以及自定义兼容端点的凭证、连接状态、模型列表和额度
- **对外渠道**：一个渠道暴露一个模型，内部可绑定多个提供商和各自的上游模型；支持优先级主备与加权轮询
- **故障切换**：429 冷却切换、401/403 标记鉴权失败、400 按 body 细分；全不可用返回聚合人话错误
- **额度查询**：支持 DeepSeek/Kimi 账户余额、z.ai/BigModel/MiniMax Coding 套餐额度、Command Code 额度和 OpenRouter Credits，也可配置自定义查询地址
- **协议保真**：canonical=Anthropic；请求内容字段不深度解析原样传输；未知 block/字段保留；`anthropic-beta`/`anthropic-version` 头白名单透传
- **用量统计**：SQLite 记录对外渠道、实际提供商、对外/上游模型；渠道汇总支持今天/7天/30天/全部，提供商页显示今日 Token
- **托盘常驻**：关窗口不退出

按个人工具裁剪（不做）：配额前置预判（只信 429）、分组多租户、图表、单提供商多密钥。

## 快速开始

### 桌面模式

```
wails build
build\bin\OneProxy.exe
```

界面里先在「提供商」添加账户并测试/查询额度，再在「渠道」创建对外模型并绑定一个或多个提供商。

### Command Code 与 Ollama Cloud

- **cmdc-proxy**：先启动 [cmdc-proxy](https://github.com/werbenhu/cmdc-proxy)，在 OneProxy 的「提供商」选择 `cmdc-proxy（本地代理）` 预设。默认地址 `http://127.0.0.1:55990/v1`，API Key 填 cmdc-proxy 的客户端密钥。OneProxy 的渠道可将对外模型映射为 `glm-5` 等 cmdc-proxy 支持的模型。
- **额度**：cmdc-proxy 的客户端密钥可能无法查询 Command Code 原始额度。若使用客户端密钥，在「额度查询设置」填入 Command Code 原始 `user_` Key 作为专用查询密钥。
- **官方 Command Code Provider API**：分别提供 OpenAI Chat/Responses 和 Anthropic Messages 端点，OneProxy 为此提供两个预设；开放模型与 Claude 模型须选择对应协议。Command Code CLI 使用的 `/alpha/generate` 是专有协议，与这些兼容端点不同。官方文档称 Go 套餐不开放 Provider API，其他套餐的权限以账户为准。
- **Ollama Cloud**：选择 `Ollama Cloud` 预设，填 Ollama API Key；默认地址 `https://ollama.com/v1`，渠道中的上游模型填账户实际可用的云端模型 ID。

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
  "providers": [
    {
      "id": "pv-kimi-a",
      "name": "Kimi 套餐 A",
      "vendor": "kimi",
      "type": "anthropic-compat",
      "baseUrl": "https://api.moonshot.cn/anthropic",
      "apiKey": "sk-xxx",
      "balanceKind": "moonshot",
      "enabled": true
    },
    {
      "id": "pv-kimi-b",
      "name": "Kimi 套餐 B（备用）",
      "vendor": "kimi",
      "type": "anthropic-compat",
      "baseUrl": "https://api.moonshot.cn/anthropic",
      "apiKey": "sk-yyy",
      "balanceKind": "moonshot",
      "enabled": true
    }
  ],
  "channels": [
    {
      "id": "ch-claude-sonnet",
      "name": "Claude Sonnet",
      "model": "claude-sonnet-4-6",
      "strategy": "priority",
      "targets": [
        {"providerId": "pv-kimi-a", "upstreamModel": "kimi-k3", "priority": 10, "weight": 1, "enabled": true},
        {"providerId": "pv-kimi-b", "upstreamModel": "kimi-k3", "priority": 5, "weight": 1, "enabled": true}
      ],
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

调试直连语法：`model=pv-<提供商ID>/<上游模型名>` 绕过渠道调度直发指定提供商；迁移后的旧 `ch-...` 提供商 ID 也兼容。OpenRouter 的 `org/model` 不受影响。

## 架构

```
proxy(入口/鉴权/SSE) → channel(对外模型/调度策略) → provider account
                              ↓                         ├─ anthropiccompat
                        router(冷却/切换)                ├─ openaicompat
usage(SQLite 记账) ← 路由埋点回调                       └─ grok
```

- 新增标准兼容供应商：在提供商页选「自定义」预设填 BaseURL，零代码
- 新增专有供应商：实现 `provider.Adapter` 接口 + main 里 import

Logo 源文件为 [oneproxy-logo.svg](frontend/src/assets/oneproxy-logo.svg) 和 [oneproxy-mark.svg](frontend/src/assets/oneproxy-mark.svg)；运行 `tools/generate-brand.ps1` 可同步生成 Windows 应用与托盘图标。

包结构、设计决策与裁剪理由见 [plan.md](plan.md)。

## 开发

```
go test ./...        # 后端测试
wails dev            # 桌面开发模式
wails build          # 打 Windows 包
```
