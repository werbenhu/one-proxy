# one-proxy 设计方案（个人工具版）

> 多供应商 LLM Token/套餐 中转代理 — Golang + Wails 桌面应用，**个人自用**
> 状态：待评审 | 日期：2026-09-25 | 领域模型借鉴 One API；范围按个人工具裁剪

---

## 1. 定位与目标

自己用的本地代理：把手里的 LLM 供应商（订阅 coding plan + 按 token 计费 API）合并成一套本地 API，多个同类套餐自动互备。

**做**：
- 对外入口：Anthropic Messages（`/v1/messages`，主力，Claude Code 用）+ OpenAI Chat（`/v1/chat/completions`）+ `GET /v1/models`；OpenAI Responses 入口随 Grok 阶段提供，通用 Responses 转换不承诺
- `anthropic-beta` / `anthropic-version` 等头**随请求白名单透传**（thinking、长上下文、细粒度工具不静默降级）
- 渠道路由：模型名 → 候选渠道，优先级主备，429/凭据失效/5xx 自动切换，冷却
- 用量记账：每渠道 × 每模型 token 消耗，按天/周/月/总量
- 模型别名 = `Channel.ModelMapping`（对外名 → 上游模型名）
- Grok：整体移植 grok-proxy（OAuth 自动刷新 + Responses 上游 + 入口协议一跳直通）

**不做（个人工具裁剪）**：
- Weight 加权随机、`Channel.Group` 多租户分组
- 配额前置预判（V1 只信 429；不画"剩余配额"进度条）
- 余额查询进 V1（Kimi 的 balance 接口是按量现金余额，套在 coding plan 渠道上显示不相干的数）
- ECharts 图表、用量下钻、模型路由页
- 三平台打包（先只打 Windows，需要再补）

## 2. 术语规范（借鉴 One API）

> **提供商（Provider）= API 是哪一类（适配器类型 + 预设模板）；渠道（Channel）= 手上具体哪一条 API 线路。一个提供商下可以有多条渠道。**

「两个 Kimi 套餐互备」示例：

```text
提供商: kimi-coding (anthropic-compat 适配器 + 预设)
├── 渠道 Kimi套餐A  key-333  Models:["claude-sonnet-4-6"]  Mapping:{"claude-sonnet-4-6":"kimi-k3"}  Priority:10
└── 渠道 Kimi套餐B  key-444  Models:["claude-sonnet-4-6"]  Mapping:{"claude-sonnet-4-6":"kimi-k3"}  Priority:5

客户端: POST /v1/messages  model=claude-sonnet-4-6
→ 命中 A、B → 固定走 A（粘性，prompt cache 友好）→ A 429/配额尽 → 冷却并切 B → A 恢复后回到 A
```

请求模型名解析：对外模型名（渠道 `Models` 声明）→（调试语法）`ch-<id>/<model>` 直连。**仅 `ch-` 前缀才解析为直连**，避免把 OpenRouter 的 `org/model` 拆错。

## 3. 供应商调研结论（设计依据）

| 供应商 | 认证 | 端点与协议 | 余额/配额 |
|---|---|---|---|
| z.ai coding plan | API Key | `api.z.ai/api/anthropic`（Anthropic）、`/api/coding/paas/v4`（OpenAI chat）、`/api/v1`（Responses） | 无公开 API；套餐 Lite/Pro/Max = **credit 单位**（token 加权 /10000，高峰半价，MCP 另计），每 5h 2k/12k/28k，周 10k/60k/140k |
| bigmodel coding plan | API Key | `open.bigmodel.cn/api/anthropic` + 同构 openai 端点 | 无公开 API；档位结构同 z.ai，**单位未逐一核对** |
| kimi (moonshot) | API Key | `api.moonshot.cn/anthropic`（Anthropic）、`/v1`（OpenAI）；中国站/国际站 Key 不互通 | 按量现金余额有 API（`GET /v1/users/me/balance`），**与套餐配额无关**，V1 不接 |
| grok (xAI) | **OAuth Device Flow**（auth.x.ai，refresh token 自动续期）+ API Key | **OpenAI Responses 协议**：API Key → `api.x.ai/v1/responses`；OAuth → `cli-chat-proxy.grok.com/v1/responses` + grok-cli 头 | 无公开 API |
| deepseek | API Key | `api.deepseek.com`（OpenAI）、`/anthropic`（Anthropic） | 余额有 API，V1 不接（路由不依赖） |
| minimax token plan | Subscription Key | `api.minimax.io|cn/anthropic` + OpenAI 端点；5h+周双窗口 | 无公开 API；**配额数值/单位未核对，不照抄 z.ai** |
| openrouter | API Key | `openrouter.ai/api/v1`（OpenAI chat，模型名 `org/model`） | credits API 需 management key，V1 不接 |
| opencode zen / commandcode | API Key | 各自 OpenAI/Anthropic 兼容端点 | 无公开 API |

**关键结论**：
1. 认证只有两种形态：API Key Bearer（绝大多数）+ OAuth Device Flow（仅 xAI）
2. 除 Grok 外全是标准 OpenAI/Anthropic 兼容端点 → 两个通用适配器全覆盖，无需为每家写代码
3. **一个 Key 一条渠道**：coding plan 只走 Anthropic 端点建渠道，不为同一 Key 再建 Responses/chat 渠道（否则额度被记两份，也没有实际用途）

## 4. 总体架构与数据流

```
┌─────────────────────────── Wails 桌面应用（托盘常驻）─────────────────┐
│  frontend (Vue3+TS)   ←→  service (Wails bindings)                   │
└──────────────────────────────┼────────────────────────────────────────┘
┌──────────────────────────────▼───────────────────────────────────────┐
│  proxy (HTTP Server, 127.0.0.1:8280)                                 │
│  /v1/messages  /v1/chat/completions  /v1/models (+/v1/responses 随M5)│
└───────┬──────────────────────────────────────────────────────────────┘
┌───────▼────────┐   ┌───────────────────────────────────────────────┐
│ router         │→ │ adapters                                      │
│ 模型→候选渠道   │   │ anthropic-compat │ openai-compat │ grok      │
│ 优先级主备      │   │ (恒等重序列化)    │ (走 convert)  │ (直通+OAuth)│
│ 冷却(内存态)    │   └───────────────────────────────────────────────┘
└───────┬────────┘                  │
┌───────▼─────────────┐  ┌──────────────┐  ┌────────────────┐
│ usage 统计 (SQLite)  │  │ config (JSON) │  │ protocol/convert│
└─────────────────────┘  └──────────────┘  └────────────────┘
```

**数据流（顺序是本版核心修正：先路由，再决定转不转）**：

```
客户端(协议P) → 入口handler: 鉴权 + 读原始 body，记住 P（此时不做任何转换）
  → router: 模型名 → 候选渠道(声明该模型 ∧ 启用 ∧ 未冷却/未auth-failed)
          → 按 Priority 取最高可用者（主），失败时按序取备
  → 适配器实现 RawForwardCapable ∧ 声明支持 P
      → 是: ForwardRaw(P, 原始body) 一跳直通
           ModelMapping 在这条路径上改 raw JSON 的 "model" 字段（不动 canonical 结构体）
      → 否: convert: P → canonical(Anthropic) → adapter.Invoke/Stream → convert 回 P
  → usage 异步记录（直通路径 tee 一份字节流旁路提取，不侵入转换逻辑）
```

## 5. 关键设计决策

### 5.1 协议处理：canonical Anthropic + 直通逃生舱 + 恒等重序列化

- **canonical = Anthropic Messages**：coding plan 生态以 Claude Code 为主，Anthropic 格式对 tools/thinking/缓存表达最完整；Anthropic 入口 → Anthropic 上游零损耗。
- **直通逃生舱**：Grok 上游是 OpenAI Responses，grok-proxy 已验证「入口协议 → Responses」一跳转换。适配器可选实现 `RawForwardCapable`，router 在 convert 之前检查（见 §4 数据流），Grok 三种入口全部一跳直达，不存在双重转换。
- **Anthropic 入口的 anthropic-compat 不做原始字节拷贝，做结构化后的恒等重序列化**：解析 → 改 model → 序列化。未知字段保留做到 **content block 一级**（Claude Code 丢的是 block 里的新类型，只挂顶层不够）。
- **快照测试锁住**：`system` / `tool_use` / `thinking` / `cache_control` 四类关键结构的往返保真。
- **canonical 携带 header bag**：入口的 `anthropic-beta`、`anthropic-version` 等白名单头随请求传给上游。
- 转换器只做 V1 需要的：`openai-chat ↔ anthropic`（双向，请求/响应/SSE），含多模态：`image_url`(chat) ↔ `image` block(anthropic) 双向映射
- **大 payload 流式处理**：图片 base64 请求体可达数 MB，body 读取、SSE 转发、tee 旁路提取 usage 一律按流处理，禁止整体读入内存拼接
- **视频不在范围**：Anthropic/OpenAI 协议均无标准 video block；同协议透传时供应商私有 block 靠 block 级保真自然通过，跨协议转换不承诺

### 5.2 模块化：够用即可，不做平台

- 三个适配器（anthropic-compat / openai-compat / grok）**写死在 main 里 import**，不搞动态注册平台。
- 预设 = 数据文件（类型 + 默认 baseUrl + 推荐模型映射 + 文档链接），**只保留实际在用的账号对应的预设**，删改成本为零。新增标准兼容供应商 = custom-anthropic / custom-openai 预设填 baseUrl，零代码。
- `provider` 包只定义接口与渠道状态，不 import 任何适配器（依赖倒置保留，这是代码整洁问题不是平台野心）。

### 5.3 渠道选择与故障切换：优先级主备，粘性，内存态冷却

**候选**：`Models` 包含请求模型名 ∧ 渠道启用 ∧ 状态可用。

**选择**：按 `Priority` 降序，固定取最高可用的一条（**无权重随机**——个人几条线用主备即可；粘性保证同一会话的 tool 循环落在同一供应商，prompt cache 不作废）。

**状态与切换**（全部内存态，重启清零，不做持久化状态机）：
- **冷却**：上游 429/配额类错误 → 冷却 5 分钟，指数退避，上限 30 分钟；冷却期该渠道不进候选
- **auth-failed**：401/403 → 停止选择，UI 提示重新授权（Grok 渠道由适配器内部 refresh 续命，见 §8）
- **5xx / 网络错误** → 切下一候选，渠道不改状态
- **400 细分**：body 含 `model` / `max_tokens` 字样（模型不存在、超限）→ 允许切换一次；JSON schema 校验类 → 不切，直接返回
- **流式**：首字节写给客户端之前可安全重试；已写出则透传错误
- **全不可用**：**不强行强打**（尤其 auth-failed，强打只会得到莫名其妙的 401），聚合一条人话错误返回，如「Kimi套餐A 鉴权失败；Kimi套餐B 冷却至 14:35」

### 5.4 配额：V1 无预判，只信 429

- **不做配额前置过滤**（z.ai 套餐单位是 credit = token 加权，不是 prompt 次数；按次数预判长期偏乐观，该切不切）。仪表盘/渠道列表只展示 token 数参考，不画「剩余 80%」假进度条。
- 「Kimi A 尽了切 B」由 429 → 冷却 → 切换覆盖，这是主场景且可靠。
- 未来若要预判：credit 估算（系数进预设，偏保守），最终仍以 429 为准。届时再立项，V1 不写任何配额代码。

### 5.5 存储

- **配置**：JSON（`%AppData%/OneProxy/config.json`），凭据明文（个人本机，同 grok-proxy），V1.1 再考虑 DPAPI。
- **用量**：SQLite（`modernc.org/sqlite` 纯 Go 无 CGO）。单表请求日志，查询时 `GROUP BY`。保留默认 90 天。

### 5.6 Adapter 调用形态

不搞「Response + channel 双返回值」，拆成两个方法：`Invoke`（非流式）与 `Stream`（流式），调用方按请求的 `stream` 选一个。

## 6. 领域模型

```go
// 渠道：一条具体上游线路
type Channel struct {
    ID           string            // "ch-3f2a"（直连语法要求 ch- 前缀）
    Name         string            // "Kimi 套餐 A"
    Type         string            // anthropic-compat | openai-compat | grok
    BaseURL      string            // 上游端点
    APIKey       string            // Bearer 凭据（grok 渠道留空，Extra 存 OAuth）
    Extra        json.RawMessage   // 适配器私有配置（OAuth token、头开关等）
    Models       []string          // 对外声明的模型名
    ModelMapping map[string]string // 对外名 → 上游实际模型名，缺省同名；兼任模型别名
    Priority     int               // 越大越优先
    Enabled      bool
}
```

## 7. Go 包结构

```
one-proxy/
├── main.go / app.go            # Wails 入口（import 三个适配器）、托盘
├── internal/
│   ├── config/                 # 配置模型、校验、JSON 存取
│   ├── provider/
│   │   ├── adapter.go          # Adapter / RawForwardCapable / OAuthCapable 接口
│   │   └── status.go           # 渠道状态（ok/cooling/auth-failed，内存态）
│   ├── adapters/
│   │   ├── anthropiccompat/    # Anthropic 兼容（恒等重序列化，block 级保真）
│   │   ├── openaicompat/       # OpenAI 兼容（内部走 convert）
│   │   └── grok/               # OAuth 刷新 + Responses 直通（移植 grok-proxy）
│   ├── protocol/
│   │   ├── anthropic/          # canonical 类型（header bag + block 级 extra）
│   │   ├── openaichat/
│   │   └── convert/            # openai-chat ↔ anthropic
│   ├── router/                 # 模型→候选、优先级主备、冷却、切换、ch-/ 直连
│   ├── usage/                  # SQLite 请求日志 + 聚合查询
│   ├── proxy/                  # HTTP server：入口、鉴权、SSE、usage 埋点（tee）
│   └── service/                # Wails bindings
└── frontend/                   # Vue 3 + TS + Vite
```

依赖方向：`proxy → router → provider(接口)`；`adapters → protocol`；`service → 全部`。

## 8. 核心接口（草案）

```go
type Adapter interface {
    Invoke(ctx context.Context, req *anthropic.Request) (*anthropic.Response, error)
    Stream(ctx context.Context, req *anthropic.Request) (<-chan anthropic.Event, error)
    Models(ctx context.Context) ([]ModelInfo, error)
    NormalizeError(err error) ErrorKind
}

// 可选：入口协议一跳直通（grok 实现：chat/responses/messages → Responses）
type RawForwardCapable interface {
    SupportedProtocols() []Protocol
    ForwardRaw(ctx context.Context, protocol Protocol, body []byte, stream bool) (RawResult, error)
}

// 可选：仅 grok。Refresh 由适配器内部托管（过期前缓冲刷新 + 单飞 + invalid_grant 置 auth-failed）
type OAuthCapable interface {
    StartDeviceAuth(ctx context.Context) (DeviceAuthInfo, error)
    PollDeviceAuth(ctx context.Context, deviceCode string) error
    Refresh(ctx context.Context) error
}

// ErrorKind → 切换决策
//   Quota      → 冷却（指数退避）+ 切下一候选
//   RateLimit  → 短冷却 + 切下一候选
//   Auth       → 置 auth-failed + 切下一候选
//   Upstream   → 切下一候选，不改状态
//   BadRequest → 默认不切；body 含 model/max_tokens 时允许切一次
```

## 9. 对外 API

| 端点 | 阶段 | 说明 |
|---|---|---|
| `GET /v1/models` | M1 | 启用渠道 `Models` 去重 |
| `POST /v1/messages` | M1 | Anthropic（JSON+SSE），beta/version 头透传 |
| `POST /v1/chat/completions` | M2 | OpenAI Chat（JSON+SSE） |
| `POST /v1/responses` | M5 | 仅 Grok 直通提供；通用转换 V1.1 再说 |

- 鉴权：单一本地密钥（自动生成，同 grok-proxy），`Authorization: Bearer` / `x-api-key` 均可
- 监听 `127.0.0.1:8280`

## 10. 用量统计

单表 `request_log`：`created_at, channel_id, channel_name, model_requested(对外名), model_upstream(映射后), protocol, input/output/cache_read/cache_write_tokens, status, latency_ms, error`。

查询（service 暴露给前端）：按渠道/模型聚合 × 天/周/月/总量；渠道列表需要「今日本渠道 token + 状态」。

## 11. 前端（最小可用）

| 页面 | 内容 |
|---|---|
| 渠道管理 | 列表（状态徽标：正常/冷却/鉴权失效 + 今日 token）；添加向导（选预设→填 Key→测试连接）；Grok OAuth 向导（设备码+轮询） |
| 设置 | 监听地址/端口、本地密钥、日志保留天数 |
| 用量 | 简单表格（渠道×模型×时间范围），**不上图表** |

**托盘 M2 就上**（grok-proxy 已验证：关闭窗口代理必须还在，这件事不能放最后）：关闭窗口驻留后台，托盘菜单含退出与打开主窗口。

## 12. 实施里程碑

| 阶段 | 内容 | 验收标准 |
|---|---|---|
| M1 骨架 | Wails 初始化、config、provider 接口、anthropic-compat（恒等重序列化）、`/v1/messages` + `/v1/models`、鉴权、beta 头透传；渠道先用 config.json 手配 | Claude Code 指向本地代理，一个 kimi/z.ai 渠道流式对话正常，**带 tool 调用与 thinking 不丢** |
| M2 Chat 入口 + 托盘 | openaichat 类型 + convert、`/v1/chat/completions`；托盘常驻 | Chat 客户端可用；关窗口代理存活，托盘可退出 |
| M3 渠道路由 | router：候选、优先级主备、429 冷却切换、400 细分、全不可用聚合错误、`ch-/` 直连 | 双渠道同模型：模拟 429 后自动切备、冷却期满回主；auth-failed 渠道不被强打 |
| M4 用量 | SQLite request_log + 埋点（canonical 路径）、聚合查询 | 按渠道/模型的天/周/月/总量正确 |
| M5 Grok | 整体移植：OAuth device flow + 自动刷新 + Responses 上游 + 三入口一跳直通（复用 grok-proxy `protocol/conversation`）+ `/v1/responses` 入口 + 直通路径 tee 提取 usage | Grok 渠道三入口非流式+流式可用；token 过期自动刷新不断流；invalid_grant 置 auth-failed |
| M6 前端 + 打包 | 渠道管理 + 设置 + 用量表格 + 两个向导；**只打 Windows** | 不碰 config.json 完成所有管理；exe 可用 |

## 13. 测试策略

- `convert`：chat↔anthropic 快照；**anthropic 恒等重序列化快照**（system / tool_use / thinking / cache_control，含未知 block 类型）
- `router`：fake adapter 注入——主备切换、冷却恢复、400 细分、全不可用聚合、ch- 直连
- `adapters/grok`：直通矩阵（3 入口 × 流式/非流式）、刷新单飞与过期缓冲、invalid_grant 状态流转
- `proxy`：httptest 端到端（真实路由 + fake 上游 + 流式）

## 14. 风险与开放问题

**风险**
- 套餐配额单位各家不一（z.ai 是 credit）→ V1 只信 429，从根上规避算错
- cli-chat-proxy 端点/grok-cli 头变更 → 隔离在 grok 适配器内的常量，照抄 grok-proxy 维护节奏
- SSE 跨协议转换丢字段 → block 级 extra + 快照测试；主流链路（anthropic→anthropic）恒等零损

**V1.1 备选（均不阻塞）**
- credit 估算预判（系数进预设，偏保守）
- 余额查询（deepseek 现金余额、openrouter credits——注意与套餐渠道区分口径）
- 凭据加密（DPAPI）、macOS/Linux 打包、通用 `/v1/responses` 转换
