// 轻量 i18n + 主题：无第三方依赖，词条集中在 dict，t() 按当前语言返回。
import { ref } from 'vue'

export type Locale = 'zh' | 'en'
export type Theme = 'dark' | 'light'

export const locale = ref<Locale>('zh')
export const theme = ref<Theme>('light')

// 词条：key → { zh, en }。视图词条命名空间：nav.* / common.* / settings.* /
// providers.* / channels.* / usage.* / usageDetail.*。
const dict: Record<string, { zh: string; en: string }> = {
  'nav.providers': { zh: '提供商', en: 'Providers' },
  'nav.channels': { zh: '渠道', en: 'Channels' },
  'nav.usage': { zh: '用量', en: 'Usage' },
  'nav.clients': { zh: '客户端接入', en: 'Clients' },
  'nav.settings': { zh: '设置', en: 'Settings' },
  'common.confirmTitle': { zh: '确认操作', en: 'Confirm' },
  'common.confirm': { zh: '确认', en: 'Confirm' },
  'common.cancel': { zh: '取消', en: 'Cancel' },
  'common.close': { zh: '关闭', en: 'Close' },
  'common.save': { zh: '保存', en: 'Save' },
  'common.refresh': { zh: '刷新', en: 'Refresh' },
  'common.add': { zh: '添加', en: 'Add' },
  'common.edit': { zh: '编辑', en: 'Edit' },
  'common.delete': { zh: '删除', en: 'Delete' },
  'common.test': { zh: '测试', en: 'Test' },
  'common.copy': { zh: '复制', en: 'Copy' },
  'common.copied': { zh: '已复制', en: 'Copied' },
  'common.show': { zh: '显示', en: 'Show' },
  'common.hide': { zh: '隐藏', en: 'Hide' },
  'common.enable': { zh: '启用', en: 'Enable' },
  'common.loading': { zh: '加载中…', en: 'Loading…' },
  'common.tokens': { zh: 'tokens', en: 'tokens' },
  'settings.theme': { zh: '皮肤', en: 'Theme' },
  'settings.themeDark': { zh: '暗色', en: 'Dark' },
  'settings.themeLight': { zh: '亮色', en: 'Light' },
  'settings.language': { zh: '语言', en: 'Language' },
  'settings.base': { zh: '基础设置', en: 'General' },
  'settings.listenHost': { zh: '监听地址', en: 'Listen host' },
  'settings.listenPort': { zh: '端口', en: 'Port' },
  'settings.localKey': { zh: '本地代理密钥（客户端 API Key）', en: 'Local proxy key (client API key)' },
  'settings.retainDays': { zh: '日志保留天数', en: 'Log retention days' },
  'settings.globalProxy': { zh: '全局 HTTP 代理（可选）', en: 'Global HTTP proxy (optional)' },
  'settings.globalProxyPlaceholder': { zh: 'http://127.0.0.1:7897 或 socks5://127.0.0.1:1080', en: 'http://127.0.0.1:7897 or socks5://127.0.0.1:1080' },
  'settings.globalProxyHint': { zh: '提供商勾选「使用代理」时走此代理；留空则走系统环境代理。', en: 'Providers with "Use proxy" enabled route through this proxy; leave blank to use the system proxy.' },
  'settings.saved': { zh: '已保存。监听地址修改后重启应用生效。', en: 'Saved. Restart the app for listen-address changes to take effect.' },
  'settings.clients': { zh: '客户端接入', en: 'Client setup' },
  'clients.hint': { zh: '把对应协议的 BASE_URL 和密钥填进客户端即可；<渠道ID> 在「渠道」页查看。', en: 'Paste the BASE_URL and key for your protocol into the client; find <channel-id> on the Channels page.' },
  'settings.channelIdPlaceholder': { zh: '<渠道ID>', en: '<channel-id>' },
  'settings.openaiEndpoints': { zh: '接口路径：Chat /v1/chat/completions，Responses /v1/responses', en: 'Endpoints: Chat /v1/chat/completions, Responses /v1/responses' },
  'providers.export': { zh: '导出', en: 'Export' },
  'providers.import': { zh: '导入', en: 'Import' },
  'providers.todayTokens': { zh: '今日 Token', en: "Today's tokens" },
  'providers.detail': { zh: '详情', en: 'Details' },
  'providers.accountQuota': { zh: '账户额度', en: 'Account quota' },
  'providers.resetTime': { zh: '重置时间', en: 'Resets at' },
  'providers.refreshing': { zh: '刷新中', en: 'Refreshing…' },
  'providers.querying': { zh: '查询中', en: 'Checking' },
  'providers.query': { zh: '查询', en: 'Check' },
  'providers.queryUnsupported': { zh: '不支持查询', en: 'Not supported' },
  'providers.dragReorder': { zh: '拖拽调整顺序', en: 'Drag to reorder' },
  'providers.models': { zh: '模型', en: 'Models' },
  'providers.empty': { zh: '还没有提供商账户。先添加一个上游账户，再到渠道页绑定模型。', en: 'No provider accounts yet. Add an upstream account first, then bind models on the Channels page.' },
  'providers.availableModels': { zh: '可用模型', en: 'Available models' },
  'providers.noModels': { zh: '上游未返回模型', en: 'Upstream returned no models' },
  'providers.editTitle': { zh: '编辑提供商', en: 'Edit provider' },
  'providers.addTitle': { zh: '添加提供商', en: 'Add provider' },
  'providers.preset': { zh: '提供商预设', en: 'Provider preset' },
  'providers.manualConfig': { zh: '手动配置', en: 'Manual setup' },
  'providers.presetGrokHint': { zh: '支持两种授权：网页授权 或 xAI API Key，添加后可在编辑中切换。', en: 'Supports two authorization methods: web authorization or xAI API Key; you can switch later when editing.' },
  'providers.presetCommandCodeHint': { zh: '官方 Provider API 按模型分端点：开放模型走 OpenAI Chat / Responses，Claude 模型走 Anthropic Messages。Go 套餐不支持 API；其他可用套餐以官方账户权限为准。', en: 'The official Provider API uses per-model endpoints: open models go through OpenAI Chat / Responses, Claude models through Anthropic Messages. The Go plan does not support API access; other plans depend on your official account permissions.' },
  'providers.presetOllamaHint': { zh: '填写 Ollama Cloud API Key；通过官方 OpenAI 兼容端点接入。模型名以账户可用模型为准。', en: 'Enter your Ollama Cloud API Key; connects via the official OpenAI-compatible endpoint. Model names depend on the models available to your account.' },
  'providers.accountName': { zh: '账户名称', en: 'Account name' },
  'providers.accountNamePlaceholder': { zh: '如 Kimi Coding A', en: 'e.g. Kimi Coding A' },
  'providers.protocolType': { zh: '协议类型', en: 'Protocol type' },
  'providers.anthropicCompat': { zh: 'Anthropic 兼容', en: 'Anthropic compatible' },
  'providers.openaiCompat': { zh: 'OpenAI 兼容', en: 'OpenAI compatible' },
  'providers.baseUrlPlaceholder': { zh: '留空使用官方端点', en: 'Leave blank to use the official endpoint' },
  'providers.useProxy': { zh: '使用代理', en: 'Use proxy' },
  'providers.useProxyHint': { zh: '勾选后该提供商的请求（含授权、token 刷新、额度查询）走设置页配置的全局代理。', en: 'When enabled, this provider\'s requests (including auth, token refresh, and quota checks) go through the global proxy configured in Settings.' },
  'providers.proxyHintOnError': { zh: '提示：这看起来是网络连接问题。如果该上游在国内无法直连，请勾选「使用代理」并在设置页配置全局代理后重试。', en: 'Hint: this looks like a network issue. If the upstream is unreachable from your network, enable "Use proxy" and configure the global proxy in Settings, then retry.' },
  'providers.authMode': { zh: '授权方式', en: 'Authorization method' },
  'providers.webAuth': { zh: '网页授权', en: 'Web authorization' },
  'providers.webAuthHint': { zh: '通过 xAI 设备授权登录（grok-cli 方式），保存后点击下方「Grok 授权」完成验证。', en: 'Sign in via xAI device authorization (grok-cli style). After saving, click "Authorize Grok" below to complete verification.' },
  'providers.keepOriginal': { zh: '留空保留原值', en: 'Leave blank to keep current value' },
  'providers.viewOriginal': { zh: '查看原值', en: 'Show original' },
  'providers.quotaSettings': { zh: '额度查询设置', en: 'Quota check settings' },
  'providers.balanceKind': { zh: '余额类型', en: 'Balance type' },
  'providers.balanceNone': { zh: '不查询', en: 'Do not check' },
  'providers.balanceCommandCode': { zh: 'Command Code 额度', en: 'Command Code quota' },
  'providers.balanceDeepseek': { zh: 'DeepSeek 账户余额', en: 'DeepSeek account balance' },
  'providers.balanceGrok': { zh: 'Grok 订阅额度', en: 'Grok subscription quota' },
  'providers.balanceMoonshot': { zh: 'Kimi 开放平台余额', en: 'Kimi Open Platform balance' },
  'providers.balanceKimiCoding': { zh: 'Kimi Coding 套餐', en: 'Kimi Coding plan' },
  'providers.balanceZaiCoding': { zh: '智谱 Coding 套餐', en: 'Zhipu Coding plan' },
  'providers.balanceMinimaxCoding': { zh: 'MiniMax Coding 套餐', en: 'MiniMax Coding plan' },
  'providers.balanceCustom': { zh: '自定义 JSON 接口', en: 'Custom JSON endpoint' },
  'providers.customEndpoint': { zh: '自定义接口', en: 'Custom endpoint' },
  'providers.customEndpointPlaceholder': { zh: '可覆盖预设接口', en: 'Overrides the preset endpoint' },
  'providers.balanceKeyLabel': { zh: '额度查询专用 Key（可选）', en: 'Dedicated quota-check key (optional)' },
  'providers.balanceKeyPlaceholder': { zh: '默认使用上方 API Key', en: 'Defaults to the API Key above' },
  'providers.enableAccount': { zh: '启用此账户', en: 'Enable this account' },
  'providers.grokAuth': { zh: 'Grok 授权', en: 'Authorize Grok' },
  'providers.authCode': { zh: '授权码', en: 'Authorization code' },
  'providers.authCodeSep': { zh: '，', en: ', ' },
  'providers.openVerification': { zh: '打开验证页', en: 'Open verification page' },
  'providers.checkAfterDone': { zh: '完成后检查', en: 'Check when done' },
  'providers.exportedTo': { zh: '已导出到', en: 'Exported to' },
  'providers.exportFailed': { zh: '导出失败：', en: 'Export failed: ' },
  'providers.importFailed': { zh: '导入失败：', en: 'Import failed: ' },
  'providers.deleteConfirm': { zh: '确认删除这个提供商账户？', en: 'Delete this provider account?' },
  'providers.testOk': { zh: '连接与鉴权正常', en: 'Connection and authentication OK' },
  'providers.testFailed': { zh: '测试失败：', en: 'Test failed: ' },
  'providers.modelsFailed': { zh: '读取模型失败：', en: 'Failed to load models: ' },
  'providers.quotaFailed': { zh: '查询额度失败：', en: 'Failed to check quota: ' },
  'providers.authSuccess': { zh: '授权成功', en: 'Authorization successful' },
  'providers.tomorrow': { zh: '明天', en: 'Tomorrow' },
  'channels.add': { zh: '添加渠道', en: 'Add channel' },
  'channels.addTitle': { zh: '添加渠道', en: 'Add channel' },
  'channels.editTitle': { zh: '编辑渠道', en: 'Edit channel' },
  'channels.colStatus': { zh: '状态', en: 'Status' },
  'channels.name': { zh: '渠道名称', en: 'Name' },
  'channels.colModel': { zh: '对外模型', en: 'Public model' },
  'channels.strategy': { zh: '调度策略', en: 'Strategy' },
  'channels.targets': { zh: '内部目标', en: 'Targets' },
  'channels.colActions': { zh: '操作', en: 'Actions' },
  'channels.statusDisabled': { zh: '已禁用', en: 'Disabled' },
  'channels.statusOk': { zh: '正常', en: 'Healthy' },
  'channels.statusPartial': { zh: '部分可用', en: 'Partial' },
  'channels.statusUnavailable': { zh: '不可用', en: 'Unavailable' },
  'channels.strategyRoundRobin': { zh: '加权轮询', en: 'Weighted round-robin' },
  'channels.strategyPriority': { zh: '优先级主备', en: 'Priority failover' },
  'channels.weight': { zh: '权重', en: 'Weight' },
  'channels.empty': { zh: '还没有对外渠道。每个渠道暴露一个模型，并把请求调度到一个或多个提供商。', en: 'No channels yet. Each channel exposes a model and routes requests to one or more providers.' },
  'channels.namePlaceholder': { zh: '如 Claude Sonnet', en: 'e.g. Claude Sonnet' },
  'channels.modelLabel': { zh: '对外模型名', en: 'Public model name' },
  'channels.targetsHint': { zh: '请求会按下列规则发送到上游账户', en: 'Requests are sent to upstream accounts according to the rules below' },
  'channels.addTarget': { zh: '添加目标', en: 'Add target' },
  'channels.provider': { zh: '提供商', en: 'Provider' },
  'channels.pleaseSelect': { zh: '请选择', en: 'Select' },
  'channels.upstreamModel': { zh: '上游模型', en: 'Upstream model' },
  'channels.upstreamModelPlaceholder': { zh: '留空透传对外模型名', en: 'Leave blank to pass through the requested model' },
  'channels.priority': { zh: '优先级', en: 'Priority' },
  'channels.removeTarget': { zh: '移除目标', en: 'Remove target' },
  'channels.targetEmpty': { zh: '至少添加一个内部目标', en: 'Add at least one target' },
  'channels.enableChannel': { zh: '启用此渠道', en: 'Enable this channel' },
  'channels.nameRequired': { zh: '请填写渠道名称', en: 'Enter the channel name' },
  'channels.modelPlaceholder': { zh: '留空匹配任意模型并透传', en: 'Leave blank to match & pass through any model' },
  'channels.wildcardModel': { zh: '* 任意模型', en: '* any model' },
  'channels.targetFieldsRequired': { zh: '每个内部目标都需要选择提供商', en: 'Every target requires a provider' },
  'channels.idLabel': { zh: '渠道 ID', en: 'Channel ID' },
  'channels.idPlaceholder': { zh: '留空自动生成', en: 'Auto-generated if blank' },
  'channels.idHint': { zh: '客户端 BASE_URL 必须带上渠道 ID，如 http://host:port/渠道ID', en: 'Client BASE_URL must include the channel ID, e.g. http://host:port/<channel ID>' },
  'channels.idDuplicate': { zh: '渠道 ID 已存在', en: 'Channel ID already exists' },
  'channels.deleteConfirm': { zh: '确认删除这个对外渠道？', en: 'Delete this channel?' },
  'usage.today': { zh: '今天', en: 'Today' },
  'usage.last7Days': { zh: '最近 7 天', en: 'Last 7 days' },
  'usage.last30Days': { zh: '最近 30 天', en: 'Last 30 days' },
  'usage.all': { zh: '全部', en: 'All' },
  'usage.providerAccount': { zh: '提供商账户', en: 'Provider account' },
  'usage.publicModel': { zh: '对外模型', en: 'Requested model' },
  'usage.upstreamModel': { zh: '上游模型', en: 'Upstream model' },
  'usage.requests': { zh: '请求数', en: 'Requests' },
  'usage.inputTokens': { zh: '输入 token', en: 'Input tokens' },
  'usage.outputTokens': { zh: '输出 token', en: 'Output tokens' },
  'usage.cacheRead': { zh: '缓存读', en: 'Cache read' },
  'usage.errors': { zh: '错误', en: 'Errors' },
  'usage.emptyHint': { zh: '暂无数据（发几个请求后刷新）', en: 'No data yet (send a few requests, then refresh)' },
  'usageDetail.title': { zh: 'Token 用量', en: 'Token usage' },
  'usageDetail.today': { zh: '今日', en: 'Today' },
  'usageDetail.thisWeek': { zh: '本周', en: 'This week' },
  'usageDetail.thisMonth': { zh: '本月', en: 'This month' },
  'usageDetail.empty': { zh: '暂无用量数据', en: 'No usage data yet' },
  'usageDetail.activity': { zh: 'Token 活动', en: 'Token activity' },
  'usageDetail.daily': { zh: '每日', en: 'Daily' },
  'usageDetail.weekly': { zh: '每周', en: 'Weekly' },
  'usageDetail.cumulative': { zh: '累计', en: 'Cumulative' },
  'usageDetail.trend': { zh: '每日 Token 趋势图', en: 'Daily token trend' },
  'usageDetail.last7Days': { zh: '近 7 日', en: 'Last 7 days' },
  'usageDetail.last30Days': { zh: '近 30 日', en: 'Last 30 days' },
  'usageDetail.loadFailed': { zh: '读取用量失败：', en: 'Failed to load usage: ' },
  'usageDetail.colon': { zh: '：', en: ': ' },
  'usageDetail.total': { zh: '累计', en: 'Total' },
  'usageDetail.byModel': { zh: '按模型消耗', en: 'Usage by model' },
  'usageDetail.model': { zh: '模型', en: 'Model' },
  'usageDetail.totalTokens': { zh: '合计 token', en: 'Total tokens' },
}

// 后端配额语义词条（internal/service/balance.go 返回 labelKey/valueKey）。
registerDict({
  'quota.period.weekly': { zh: '每周额度', en: 'Weekly quota' },
  'quota.period.monthly': { zh: '每月额度', en: 'Monthly quota' },
  'quota.period.daily': { zh: '每日额度', en: 'Daily quota' },
  'quota.period.subscription': { zh: '订阅额度', en: 'Subscription quota' },
  'quota.window.5h': { zh: '5 小时', en: '5-hour' },
  'quota.window.weekly': { zh: '每周', en: 'Weekly' },
  'quota.window.generic': { zh: '窗口', en: 'Window' },
  'quota.window.days': { zh: '{0} 天窗口', en: '{0}-day window' },
  'quota.window.minutes': { zh: '{0} 分钟窗口', en: '{0}-minute window' },
  'quota.plan.timeLimit': { zh: '5 小时限额', en: '5-hour limit' },
  'quota.plan.tokensLimit': { zh: 'Token 限额', en: 'Token limit' },
  'quota.quota.total': { zh: '总额度', en: 'Total credits' },
  'quota.quota.usedAmount': { zh: '已使用', en: 'Used' },
  'quota.balance.available': { zh: '{0} 可用余额', en: '{0} available' },
  'quota.balance.toppedUp': { zh: '{0} 充值余额', en: '{0} topped up' },
  'quota.balance.granted': { zh: '{0} 赠送余额', en: '{0} granted' },
  'quota.balance.cash': { zh: '现金余额', en: 'Cash balance' },
  'quota.balance.voucher': { zh: '赠送余额', en: 'Voucher balance' },
  'quota.balance.generic': { zh: '余额', en: 'Balance' },
  'quota.balance.remaining': { zh: '剩余额度', en: 'Remaining' },
  'quota.credits.monthly': { zh: '月度额度', en: 'Monthly credits' },
  'quota.credits.purchased': { zh: '购买额度', en: 'Purchased credits' },
  'quota.credits.free': { zh: '免费额度', en: 'Free credits' },
  'quota.value.usedPercent': { zh: '已用 {0}%', en: '{0}% used' },
  'quota.value.usedOverLimit': { zh: '{0} / {1}', en: '{0} / {1}' },
  'quota.value.usedTimes': { zh: '已用 {0} 次', en: '{0} used' },
})

export function t(key: string): string {
  const entry = dict[key]
  if (!entry) return key
  return locale.value === 'en' ? entry.en : entry.zh
}

// 批量注册词条（各视图转换后合并进来）。
export function registerDict(entries: Record<string, { zh: string; en: string }>) {
  Object.assign(dict, entries)
}

export function applyTheme(value: string) {
  theme.value = value === 'light' ? 'light' : 'dark'
  document.documentElement.dataset.theme = theme.value
}

export function applyLocale(value: string) {
  locale.value = value === 'en' ? 'en' : 'zh'
}

// initUI 应用启动时根据设置初始化主题与语言。
export function initUI(settings: { theme?: string; language?: string }) {
  applyTheme(settings.theme || 'light')
  applyLocale(settings.language || 'zh')
}

const MONTHS_EN = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

// monthDayLabel 日期标签：zh「9月26日」/ en「9/26」。
export function monthDayLabel(month: number, day: number): string {
  return locale.value === 'en' ? month + '/' + day : month + '月' + day + '日'
}

// monthName 月份标签：zh「9月」/ en「Sep」。
export function monthName(month: number): string {
  return locale.value === 'en' ? MONTHS_EN[(month - 1 + 12) % 12] : month + '月'
}

// formatNumber 紧凑数字：zh「12.5万 / 1.2亿」en「125K / 1.2M」。
export function formatNumber(value: number): string {
  return new Intl.NumberFormat(locale.value === 'en' ? 'en' : 'zh-CN', { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}

// formatNumberFull 完整分组数字（悬浮提示等需要精确值的场景）。
export function formatNumberFull(value: number): string {
  return new Intl.NumberFormat(locale.value === 'en' ? 'en' : 'zh-CN').format(value)
}

// tp 带参数的词条：'{0}'、'{1}' 按顺序替换。
export function tp(key: string, ...args: (string | number)[]): string {
  let text = t(key)
  args.forEach((arg, i) => { text = text.replaceAll('{' + i + '}', String(arg)) })
  return text
}

// quotaLabel 渲染后端配额标签：语义 key 走词条，raw 或未识别回退原文。
export function quotaLabel(m: { labelKey?: string; labelArgs?: string[]; label?: string }): string {
  const key = m.labelKey
  if (key && key !== 'raw' && dict['quota.' + key]) return tp('quota.' + key, ...(m.labelArgs ?? []))
  return m.labelArgs?.[0] || m.label || ''
}

// quotaValue 渲染配额数值；无语义 key 时回退后端原样文本。
export function quotaValue(m: { valueKey?: string; valueArgs?: string[]; value?: string }): string {
  if (m.valueKey) return tp('quota.value.' + m.valueKey, ...(m.valueArgs ?? []))
  return m.value || ''
}
