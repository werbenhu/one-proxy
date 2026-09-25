<template>
  <div>
    <div class="page-head">
      <div><h2>提供商</h2></div>
      <div class="actions"><button class="primary" @click="openAdd">添加提供商</button><button @click="exportProviders">导出</button><button @click="importProviders">导入</button><button @click="refresh">刷新</button></div>
    </div>

    <div v-if="providers.length" class="provider-list">
      <article v-for="provider in providers" :key="provider.id" class="provider-row">
        <div class="provider-main">
          <div class="provider-title">
            <span v-if="!provider.enabled" class="status-dot disabled"></span>
            <span v-else-if="provider.status === 'ok'" class="status-dot ok"></span>
            <span v-else-if="provider.status === 'cooling'" class="status-dot cooling"></span>
            <span v-else class="status-dot failed"></span>
            <strong>{{ provider.name }}</strong>
          </div>
        </div>
        <div class="provider-stat token-stats">
          <div class="token-line"><span>今日</span><b>{{ formatNumber(provider.todayTokens) }}</b></div>
          <div class="token-line"><span>本周</span><b>{{ formatNumber(provider.weekTokens) }}</b></div>
          <div class="token-line"><span>本月</span><b>{{ formatNumber(provider.monthTokens) }}</b></div>
        </div>
        <div class="provider-stat balance-cell">
          <span>账户额度</span>
          <div v-if="balances[provider.id]" class="balance-result">
            <div v-if="quotaMetrics(provider.id).length" class="quota-lines">
              <div v-for="item in quotaMetrics(provider.id)" :key="item.label" class="quota-line">
                <span class="quota-label">{{ shortLabel(item.label) }}</span>
                <div class="bar"><i :class="barClass(item.percent!)" :style="{ width: item.percent + '%' }"></i><span class="bar-text">{{ item.value }}</span></div>
                <span class="quota-reset" :title="item.resetAt ? '重置时间 ' + formatTime(item.resetAt) : ''">{{ item.resetAt ? resetText(item.resetAt) : '' }}</span>
              </div>
            </div>
            <b v-else class="balance-summary">{{ balances[provider.id].summary }}</b>
            <button class="link-button refresh-button" :disabled="refreshing.has(provider.id)" @click="checkBalance(provider.id)">{{ refreshing.has(provider.id) ? '刷新中' : '刷新' }}</button>
          </div>
          <button v-else-if="provider.balanceKind || provider.balanceUrl" class="link-button refresh-button" :disabled="refreshing.has(provider.id)" @click="checkBalance(provider.id)">{{ refreshing.has(provider.id) ? '查询中' : '查询' }}</button>
          <em v-else>不支持查询</em>
        </div>
        <div class="row-actions">
          <button @click="test(provider.id)">测试</button>
          <button @click="discoverModels(provider.id)">模型</button>
          <button @click="edit(provider)">编辑</button>
          <button class="danger" @click="remove(provider.id)">删除</button>
        </div>
      </article>
    </div>
    <div v-else class="empty-state">还没有提供商账户。先添加一个上游账户，再到渠道页绑定模型。</div>

    <div v-if="modelsOpen" class="modal-mask" @click.self="modelsOpen = false">
      <div class="modal modal-small">
        <h3>可用模型</h3>
        <div v-if="modelList.length" class="model-list"><code v-for="m in modelList" :key="m">{{ m }}</code></div>
        <p v-else class="muted">上游未返回模型</p>
        <div class="actions modal-actions"><button @click="modelsOpen = false">关闭</button></div>
      </div>
    </div>

    <div v-if="modalOpen" class="modal-mask" @click.self="modalOpen = false">
      <div class="modal">
        <h3>{{ form.ID ? '编辑提供商' : '添加提供商' }}</h3>
        <div v-if="!form.ID" class="field">
          <label>提供商预设</label>
          <select v-model="presetKey" @change="applyPreset"><option value="">手动配置</option><option v-for="p in presets" :key="p.key" :value="p.key">{{ p.displayName }}</option></select>
          <small v-if="presetKey === 'grok'" class="muted">支持两种授权：网页授权 或 xAI API Key，添加后可在编辑中切换。</small>
          <small v-else-if="presetKey.startsWith('commandcode-provider')" class="muted">官方 Provider API 按模型分端点：开放模型走 OpenAI Chat / Responses，Claude 模型走 Anthropic Messages。Go 套餐不支持 API；其他可用套餐以官方账户权限为准。</small>
          <small v-else-if="presetKey === 'ollama-cloud'" class="muted">填写 Ollama Cloud API Key；通过官方 OpenAI 兼容端点接入。模型名以账户可用模型为准。</small>
        </div>
        <div class="row">
          <div class="field"><label>账户名称</label><input v-model="form.Name" placeholder="如 Kimi Coding A" /></div>
          <div class="field"><label>协议类型</label><select v-model="form.Type" @change="onTypeChange"><option value="anthropic-compat">Anthropic 兼容</option><option value="openai-compat">OpenAI 兼容</option><option value="grok">Grok</option></select></div>
        </div>
        <div class="field"><label>Base URL</label><input v-model="form.BaseURL" :placeholder="form.Type === 'grok' ? '留空使用官方端点' : 'https://...'" /></div>
        <div v-if="form.Type === 'grok'" class="field">
          <label>授权方式</label>
          <div class="segmented grok-auth-mode">
            <button type="button" :class="{ active: form.AuthMode !== 'api_key' }" @click="form.AuthMode = 'oauth'">网页授权</button>
            <button type="button" :class="{ active: form.AuthMode === 'api_key' }" @click="form.AuthMode = 'api_key'">API Key</button>
          </div>
          <small v-if="form.AuthMode !== 'api_key'" class="muted">通过 xAI 设备授权登录（grok-cli 方式），保存后点击下方「Grok 授权」完成验证。</small>
        </div>
        <div v-if="form.Type !== 'grok' || form.AuthMode === 'api_key'" class="field">
          <label>API Key <span v-if="form.ID" class="muted">留空保留原值</span></label>
          <div class="key-field">
            <input v-model="keyInput" :type="keyVisible ? 'text' : 'password'" placeholder="sk-..." />
            <button v-if="form.ID" type="button" class="icon-btn eye-btn" :title="keyVisible ? '隐藏' : '查看原值'" @click="toggleKey">
              <svg v-if="keyVisible" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
              <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
            </button>
          </div>
        </div>
        <details class="advanced">
          <summary>额度查询设置</summary>
          <div class="row">
            <div class="field"><label>余额类型</label><select v-model="form.BalanceKind"><option value="">不查询</option><option value="commandcode">Command Code 额度</option><option value="deepseek">DeepSeek 账户余额</option><option value="grok">Grok 订阅额度</option><option value="moonshot">Kimi 开放平台余额</option><option value="kimi-coding">Kimi Coding 套餐</option><option value="zai-coding">智谱 Coding 套餐</option><option value="minimax-coding">MiniMax Coding 套餐</option><option value="openrouter">OpenRouter Credits</option><option value="custom">自定义 JSON 接口</option></select></div>
            <div class="field"><label>自定义接口</label><input v-model="form.BalanceURL" placeholder="可覆盖预设接口" /></div>
          </div>
          <div v-if="form.BalanceKind" class="field">
            <label>额度查询专用 Key（可选）</label>
            <div class="key-field">
              <input v-model="balanceKeyInput" :type="balanceKeyVisible ? 'text' : 'password'" :placeholder="form.ID ? '留空保留原值' : '默认使用上方 API Key'" />
              <button v-if="form.ID" type="button" class="icon-btn eye-btn" :title="balanceKeyVisible ? '隐藏' : '查看原值'" @click="toggleBalanceKey">
                <svg v-if="balanceKeyVisible" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
              </button>
            </div>
          </div>
        </details>
        <label class="check"><input type="checkbox" v-model="form.Enabled" />启用此账户</label>
        <p v-if="formError" class="error-text">{{ formError }}</p>
        <div class="actions modal-actions"><button class="primary" @click="save">保存</button><button v-if="form.Type === 'grok' && form.ID && form.AuthMode !== 'api_key'" @click="startOAuth">Grok 授权</button><button @click="modalOpen = false">取消</button></div>
        <div v-if="oauthInfo" class="oauth-box">授权码 <b>{{ oauthInfo.userCode }}</b>，<a :href="oauthInfo.verificationUriComplete || oauthInfo.verificationUri" target="_blank">打开验证页</a><button @click="pollOAuth">完成后检查</button></div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { app, type BalanceView, type DeviceAuthInfo, type PresetView, type ProviderInput, type ProviderView } from '../api'
import { confirmDialog, toast } from '../ui'

const providers = ref<ProviderView[]>([])
const presets = ref<PresetView[]>([])
const balances = ref<Record<string, BalanceView>>({})
const refreshing = ref(new Set<string>())
const modelsOpen = ref(false)
const modelList = ref<string[]>([])
const modalOpen = ref(false)
const presetKey = ref('')
const formError = ref('')
const oauthInfo = ref<DeviceAuthInfo | null>(null)
const form = ref<ProviderInput>(emptyForm())
const KEY_MASK = '**********************'
const keyInput = ref('')
const keyVisible = ref(false)
const balanceKeyInput = ref('')
const balanceKeyVisible = ref(false)

function emptyForm(): ProviderInput { return { ID: '', Name: '', Vendor: '', Type: 'anthropic-compat', BaseURL: '', APIKey: '', Extra: null, AuthMode: '', BalanceKind: '', BalanceURL: '', BalanceKey: '', Enabled: true } }
function onTypeChange() { form.value.AuthMode = form.value.Type === 'grok' ? (form.value.AuthMode || 'oauth') : '' }
async function refresh() { providers.value = await app().GetProviders(); autoCheckBalances() }
async function exportProviders() {
  try {
    const path = await app().ExportProviders()
    if (path) toast('已导出到 ' + path)
  } catch (e) { toast('导出失败：' + String(e), 'error', 5000) }
}
async function importProviders() {
  try {
    const msg = await app().ImportProviders()
    if (msg) { toast(msg); await refresh() }
  } catch (e) { toast('导入失败：' + String(e), 'error', 5000) }
}
onMounted(async () => { await refresh(); presets.value = await app().GetPresets() })

function autoCheckBalances() {
  for (const p of providers.value) {
    if ((p.balanceKind || p.balanceUrl) && !balances.value[p.id]) fetchBalance(p.id)
  }
}

function openAdd() { form.value = emptyForm(); presetKey.value = ''; formError.value = ''; oauthInfo.value = null; resetKeys('', ''); modalOpen.value = true }
function applyPreset() {
  const p = presets.value.find(item => item.key === presetKey.value); if (!p) return
  form.value = { ...form.value, Name: p.displayName, Vendor: p.vendor, Type: p.type, BaseURL: p.baseUrl, AuthMode: p.type === 'grok' ? 'oauth' : '', BalanceKind: p.balanceKind || '', BalanceURL: p.balanceUrl || '' }
}
function resetKeys(apiKey: string, balanceKey: string) {
  keyInput.value = apiKey; keyVisible.value = false
  balanceKeyInput.value = balanceKey; balanceKeyVisible.value = false
  fetchedKeys.value = null
}
function edit(p: ProviderView) {
  form.value = { ID: p.id, Name: p.name, Vendor: p.vendor, Type: p.type, BaseURL: p.baseUrl, APIKey: '', Extra: null, AuthMode: p.authMode || '', BalanceKind: p.balanceKind, BalanceURL: p.balanceUrl, BalanceKey: '', Enabled: p.enabled }
  resetKeys(p.apiKeyHint ? KEY_MASK : '', p.balanceKeyHint ? KEY_MASK : '')
  presetKey.value = ''; formError.value = ''; oauthInfo.value = null; modalOpen.value = true
}
const fetchedKeys = ref<{ apiKey: string; balanceKey: string } | null>(null)
async function revealKey(input: typeof keyInput, visible: typeof keyVisible) {
  if (visible.value) { visible.value = false; return }
  if (input.value === KEY_MASK) {
    try {
      const keys = fetchedKeys.value ?? (fetchedKeys.value = await app().GetProviderKeys(form.value.ID))
      input.value = input === keyInput ? keys.apiKey : keys.balanceKey
    } catch (e) { toast(String(e), 'error'); return }
  }
  visible.value = true
}
function toggleKey() { revealKey(keyInput, keyVisible) }
function toggleBalanceKey() { revealKey(balanceKeyInput, balanceKeyVisible) }
async function save() {
  form.value.APIKey = keyInput.value === KEY_MASK ? '' : keyInput.value
  form.value.BalanceKey = balanceKeyInput.value === KEY_MASK ? '' : balanceKeyInput.value
  try { await app().SaveProvider(form.value); modalOpen.value = false; await refresh() } catch (e) { formError.value = String(e) }
}
async function remove(id: string) { if (!await confirmDialog('确认删除这个提供商账户？')) return; try { await app().DeleteProvider(id); await refresh() } catch (e) { toast(String(e), 'error') } }
async function test(id: string) { try { await app().TestProvider(id); toast('连接与鉴权正常') } catch (e) { toast('测试失败：' + String(e), 'error', 5000) } }
async function discoverModels(id: string) { try { const models = await app().GetProviderModels(id); modelList.value = models.map(m => m.id); modelsOpen.value = true } catch (e) { toast('读取模型失败：' + String(e), 'error', 5000) } }
async function fetchBalance(id: string) { try { balances.value[id] = await app().GetProviderBalance(id) } catch { /* 自动查询失败时保持「查询」按钮 */ } }
async function checkBalance(id: string) {
  refreshing.value = new Set(refreshing.value).add(id)
  try { balances.value[id] = await app().GetProviderBalance(id) } catch (e) { toast('查询额度失败：' + String(e), 'error', 5000) } finally {
    const next = new Set(refreshing.value); next.delete(id); refreshing.value = next
  }
}
function quotaMetrics(id: string) {
  return (balances.value[id]?.details ?? []).filter(item => item.percent !== undefined).slice(0, 3)
}
function shortLabel(label: string) { return label.replace(/窗口|额度|限额/g, '') }
function barClass(percent: number) { return percent >= 90 ? 'danger' : percent >= 70 ? 'warn' : '' }
async function startOAuth() { try { oauthInfo.value = await app().StartGrokDeviceAuth(form.value.ID) } catch (e) { formError.value = String(e) } }
async function pollOAuth() { if (!oauthInfo.value) return; try { await app().CompleteGrokDeviceAuth(form.value.ID, oauthInfo.value.deviceCode); oauthInfo.value = null; toast('授权成功') } catch (e) { formError.value = String(e) } }
function formatNumber(value: number) { return new Intl.NumberFormat('zh-CN').format(value) }
function formatTime(value: string) { return value ? new Date(value).toLocaleString() : '' }
function resetText(value: string) {
  const reset = new Date(value)
  const now = new Date()
  const time = reset.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  if (reset.toDateString() === now.toDateString()) return time
  const tomorrow = new Date(now); tomorrow.setDate(now.getDate() + 1)
  if (reset.toDateString() === tomorrow.toDateString()) return '明天 ' + time
  return (reset.getMonth() + 1) + '-' + reset.getDate() + ' ' + time
}
</script>
