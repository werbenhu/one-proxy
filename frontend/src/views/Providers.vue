<template>
  <div>
    <div class="page-head">
      <div><h2>{{ t('nav.providers') }}</h2></div>
      <div class="actions"><button class="primary" @click="openAdd">{{ t('common.add') }}</button><button @click="exportProviders">{{ t('providers.export') }}</button><button @click="importProviders">{{ t('providers.import') }}</button><button @click="refresh">{{ t('common.refresh') }}</button></div>
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
        <div class="provider-stat">
          <span>{{ t('providers.todayTokens') }}</span>
          <b>{{ formatNumber(provider.todayTokens) }}</b>
          <button class="link-button" @click="openUsage(provider)">{{ t('providers.detail') }}</button>
        </div>
        <div class="provider-stat balance-cell">
          <span>{{ t('providers.accountQuota') }}</span>
          <div v-if="balances[provider.id]" class="balance-result">
            <div v-if="quotaMetrics(provider.id).length" class="quota-lines">
              <div v-for="item in quotaMetrics(provider.id)" :key="item.label" class="quota-line">
                <span class="quota-label">{{ shortLabel(item.label) }}</span>
                <div class="bar"><i :class="barClass(item.percent!)" :style="{ width: item.percent + '%' }"></i><span class="bar-text">{{ item.value }}</span></div>
                <span class="quota-reset" :title="item.resetAt ? t('providers.resetTime') + ' ' + formatTime(item.resetAt) : ''">{{ item.resetAt ? resetText(item.resetAt) : '' }}</span>
              </div>
            </div>
            <b v-else class="balance-summary">{{ balances[provider.id].summary }}</b>
            <button class="link-button refresh-button" :disabled="refreshing.has(provider.id)" @click="checkBalance(provider.id)">{{ refreshing.has(provider.id) ? t('providers.refreshing') : t('common.refresh') }}</button>
          </div>
          <button v-else-if="provider.balanceKind || provider.balanceUrl" class="link-button refresh-button" :disabled="refreshing.has(provider.id)" @click="checkBalance(provider.id)">{{ refreshing.has(provider.id) ? t('providers.querying') : t('providers.query') }}</button>
          <em v-else>{{ t('providers.queryUnsupported') }}</em>
        </div>
        <div class="row-actions">
          <button @click="test(provider.id)">{{ t('common.test') }}</button>
          <button @click="discoverModels(provider.id)">{{ t('providers.models') }}</button>
          <button @click="edit(provider)">{{ t('common.edit') }}</button>
          <button class="danger" @click="remove(provider.id)">{{ t('common.delete') }}</button>
        </div>
      </article>
    </div>
    <div v-else class="empty-state">{{ t('providers.empty') }}</div>

    <UsageDetail v-if="usageFor" :provider="usageFor" @close="usageFor = null" />

    <div v-if="modelsOpen" class="modal-mask" @click.self="modelsOpen = false">
      <div class="modal modal-small">
        <h3>{{ t('providers.availableModels') }}</h3>
        <div v-if="modelList.length" class="model-list"><code v-for="m in modelList" :key="m">{{ m }}</code></div>
        <p v-else class="muted">{{ t('providers.noModels') }}</p>
        <div class="actions modal-actions"><button @click="modelsOpen = false">{{ t('common.close') }}</button></div>
      </div>
    </div>

    <div v-if="modalOpen" class="modal-mask">
      <div class="modal">
        <h3>{{ form.ID ? t('providers.editTitle') : t('providers.addTitle') }}</h3>
        <div v-if="!form.ID" class="field">
          <label>{{ t('providers.preset') }}</label>
          <select v-model="presetKey" @change="applyPreset"><option value="">{{ t('providers.manualConfig') }}</option><option v-for="p in presets" :key="p.key" :value="p.key">{{ p.displayName }}</option></select>
          <small v-if="presetKey === 'grok'" class="muted">{{ t('providers.presetGrokHint') }}</small>
          <small v-else-if="presetKey.startsWith('commandcode-provider')" class="muted">{{ t('providers.presetCommandCodeHint') }}</small>
          <small v-else-if="presetKey === 'ollama-cloud'" class="muted">{{ t('providers.presetOllamaHint') }}</small>
        </div>
        <div class="row">
          <div class="field"><label>{{ t('providers.accountName') }}</label><input v-model="form.Name" :placeholder="t('providers.accountNamePlaceholder')" /></div>
          <div class="field"><label>{{ t('providers.protocolType') }}</label><select v-model="form.Type" @change="onTypeChange"><option value="anthropic-compat">{{ t('providers.anthropicCompat') }}</option><option value="openai-compat">{{ t('providers.openaiCompat') }}</option><option value="grok">Grok</option></select></div>
        </div>
        <div class="field"><label>Base URL</label><input v-model="form.BaseURL" :placeholder="form.Type === 'grok' ? t('providers.baseUrlPlaceholder') : 'https://...'" /></div>
        <div class="field">
          <label class="check"><input type="checkbox" v-model="form.UseProxy" />{{ t('providers.useProxy') }}</label>
          <small class="muted">{{ t('providers.useProxyHint') }}</small>
        </div>
        <div v-if="form.Type === 'grok'" class="field">
          <label>{{ t('providers.authMode') }}</label>
          <div class="segmented grok-auth-mode">
            <button type="button" :class="{ active: form.AuthMode !== 'api_key' }" @click="form.AuthMode = 'oauth'">{{ t('providers.webAuth') }}</button>
            <button type="button" :class="{ active: form.AuthMode === 'api_key' }" @click="form.AuthMode = 'api_key'">API Key</button>
          </div>
          <small v-if="form.AuthMode !== 'api_key'" class="muted">{{ t('providers.webAuthHint') }}</small>
        </div>
        <div v-if="form.Type !== 'grok' || form.AuthMode === 'api_key'" class="field">
          <label>API Key <span v-if="form.ID" class="muted">{{ t('providers.keepOriginal') }}</span></label>
          <div class="key-field">
            <input v-model="keyInput" :type="keyVisible ? 'text' : 'password'" placeholder="sk-..." />
            <button v-if="form.ID" type="button" class="icon-btn eye-btn" :title="keyVisible ? t('common.hide') : t('providers.viewOriginal')" @click="toggleKey">
              <svg v-if="keyVisible" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
              <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
            </button>
          </div>
        </div>
        <details class="advanced">
          <summary>{{ t('providers.quotaSettings') }}</summary>
          <div class="row">
            <div class="field"><label>{{ t('providers.balanceKind') }}</label><select v-model="form.BalanceKind"><option value="">{{ t('providers.balanceNone') }}</option><option value="commandcode">{{ t('providers.balanceCommandCode') }}</option><option value="deepseek">{{ t('providers.balanceDeepseek') }}</option><option value="grok">{{ t('providers.balanceGrok') }}</option><option value="moonshot">{{ t('providers.balanceMoonshot') }}</option><option value="kimi-coding">{{ t('providers.balanceKimiCoding') }}</option><option value="zai-coding">{{ t('providers.balanceZaiCoding') }}</option><option value="minimax-coding">{{ t('providers.balanceMinimaxCoding') }}</option><option value="openrouter">OpenRouter Credits</option><option value="custom">{{ t('providers.balanceCustom') }}</option></select></div>
            <div class="field"><label>{{ t('providers.customEndpoint') }}</label><input v-model="form.BalanceURL" :placeholder="t('providers.customEndpointPlaceholder')" /></div>
          </div>
          <div v-if="form.BalanceKind" class="field">
            <label>{{ t('providers.balanceKeyLabel') }}</label>
            <div class="key-field">
              <input v-model="balanceKeyInput" :type="balanceKeyVisible ? 'text' : 'password'" :placeholder="form.ID ? t('providers.keepOriginal') : t('providers.balanceKeyPlaceholder')" />
              <button v-if="form.ID" type="button" class="icon-btn eye-btn" :title="balanceKeyVisible ? t('common.hide') : t('providers.viewOriginal')" @click="toggleBalanceKey">
                <svg v-if="balanceKeyVisible" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>
                <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>
              </button>
            </div>
          </div>
        </details>
        <label class="check"><input type="checkbox" v-model="form.Enabled" />{{ t('providers.enableAccount') }}</label>
        <p v-if="formError" class="error-text">{{ formError }}</p>
        <div class="actions modal-actions"><button class="primary" @click="save">{{ t('common.save') }}</button><button v-if="form.Type === 'grok' && form.ID && form.AuthMode !== 'api_key'" @click="startOAuth">{{ t('providers.grokAuth') }}</button><button @click="modalOpen = false">{{ t('common.cancel') }}</button></div>
        <div v-if="oauthInfo" class="oauth-box">{{ t('providers.authCode') }} <b>{{ oauthInfo.userCode }}</b>{{ t('providers.authCodeSep') }}<a :href="oauthInfo.verificationUriComplete || oauthInfo.verificationUri" target="_blank">{{ t('providers.openVerification') }}</a><button @click="pollOAuth">{{ t('providers.checkAfterDone') }}</button></div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { app, type BalanceView, type DeviceAuthInfo, type PresetView, type ProviderInput, type ProviderView } from '../api'
import { confirmDialog, toast } from '../ui'
import { t, monthDayLabel } from '../i18n'
import UsageDetail from '../components/UsageDetail.vue'

const providers = ref<ProviderView[]>([])
const presets = ref<PresetView[]>([])
const balances = ref<Record<string, BalanceView>>({})
const refreshing = ref(new Set<string>())
const modelsOpen = ref(false)
const modelList = ref<string[]>([])
const usageFor = ref<ProviderView | null>(null)
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

function emptyForm(): ProviderInput { return { ID: '', Name: '', Vendor: '', Type: 'anthropic-compat', BaseURL: '', APIKey: '', Extra: null, AuthMode: '', BalanceKind: '', BalanceURL: '', BalanceKey: '', UseProxy: false, Enabled: true } }
function onTypeChange() { form.value.AuthMode = form.value.Type === 'grok' ? (form.value.AuthMode || 'oauth') : '' }
async function refresh() { providers.value = await app().GetProviders(); autoCheckBalances() }
async function exportProviders() {
  try {
    const path = await app().ExportProviders()
    if (path) toast(t('providers.exportedTo') + ' ' + path)
  } catch (e) { toast(t('providers.exportFailed') + String(e), 'error', 5000) }
}
async function importProviders() {
  try {
    const msg = await app().ImportProviders()
    if (msg) { toast(msg); await refresh() }
  } catch (e) { toast(t('providers.importFailed') + String(e), 'error', 5000) }
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
  form.value = { ...form.value, Name: p.displayName, Vendor: p.vendor, Type: p.type, BaseURL: p.baseUrl, AuthMode: p.type === 'grok' ? 'oauth' : '', BalanceKind: p.balanceKind || '', BalanceURL: p.balanceUrl || '' }}
function resetKeys(apiKey: string, balanceKey: string) {
  keyInput.value = apiKey; keyVisible.value = false
  balanceKeyInput.value = balanceKey; balanceKeyVisible.value = false
  fetchedKeys.value = null
}
function edit(p: ProviderView) {
  form.value = { ID: p.id, Name: p.name, Vendor: p.vendor, Type: p.type, BaseURL: p.baseUrl, APIKey: '', Extra: null, AuthMode: p.authMode || '', BalanceKind: p.balanceKind, BalanceURL: p.balanceUrl, BalanceKey: '', UseProxy: p.useProxy, Enabled: p.enabled }
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
async function remove(id: string) { if (!await confirmDialog(t('providers.deleteConfirm'))) return; try { await app().DeleteProvider(id); await refresh() } catch (e) { toast(String(e), 'error') } }
async function test(id: string) { try { await app().TestProvider(id); toast(t('providers.testOk')) } catch (e) {
  const p = providers.value.find(x => x.id === id)
  const hint = p && !p.useProxy && isNetworkError(e) ? '\n' + t('providers.proxyHintOnError') : ''
  toast(t('providers.testFailed') + String(e) + hint, 'error', 6000)
} }
async function discoverModels(id: string) { try { const models = await app().GetProviderModels(id); modelList.value = models.map(m => m.id); modelsOpen.value = true } catch (e) { toast(t('providers.modelsFailed') + String(e), 'error', 5000) } }
function openUsage(p: ProviderView) { usageFor.value = p }
async function fetchBalance(id: string) { try { balances.value[id] = await app().GetProviderBalance(id) } catch { /* 自动查询失败时保持「查询」按钮 */ } }
async function checkBalance(id: string) {
  refreshing.value = new Set(refreshing.value).add(id)
  try { balances.value[id] = await app().GetProviderBalance(id) } catch (e) { toast(t('providers.quotaFailed') + String(e), 'error', 5000) } finally {
    const next = new Set(refreshing.value); next.delete(id); refreshing.value = next
  }
}
function quotaMetrics(id: string) {
  return (balances.value[id]?.details ?? []).filter(item => item.percent !== undefined).slice(0, 3)
}
function shortLabel(label: string) { return label.replace(/窗口|额度|限额/g, '') }
function barClass(percent: number) { return percent >= 90 ? 'danger' : percent >= 70 ? 'warn' : '' }
function isNetworkError(e: unknown): boolean {
  const msg = String(e).toLowerCase()
  return /\beof\b|connection reset|timeout|timed out|refused|no such host|network is unreachable|tls handshake/.test(msg)
}
function withProxyHint(e: unknown): string {
  const msg = String(e)
  if (!form.value.UseProxy && isNetworkError(e)) return msg + '\n' + t('providers.proxyHintOnError')
  return msg
}
async function startOAuth() { try { oauthInfo.value = await app().StartGrokDeviceAuth(form.value.ID) } catch (e) { formError.value = withProxyHint(e) } }
async function pollOAuth() { if (!oauthInfo.value) return; try { await app().CompleteGrokDeviceAuth(form.value.ID, oauthInfo.value.deviceCode); oauthInfo.value = null; toast(t('providers.authSuccess')) } catch (e) { formError.value = withProxyHint(e) } }
function formatNumber(value: number) { return new Intl.NumberFormat('zh-CN').format(value) }
function formatTime(value: string) { return value ? new Date(value).toLocaleString() : '' }
function resetText(value: string) {
  const reset = new Date(value)
  const now = new Date()
  const time = reset.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  if (reset.toDateString() === now.toDateString()) return time
  const tomorrow = new Date(now); tomorrow.setDate(now.getDate() + 1)
  if (reset.toDateString() === tomorrow.toDateString()) return t('providers.tomorrow') + ' ' + time
  return monthDayLabel(reset.getMonth() + 1, reset.getDate()) + ' ' + time
}
</script>
