<template>
  <div>
    <h2>渠道</h2>
    <div class="actions">
      <button class="primary" @click="openAdd">添加渠道</button>
      <button @click="refresh">刷新</button>
    </div>
    <table>
      <thead>
        <tr>
          <th>状态</th><th>名称</th><th>类型</th><th>对外模型</th><th>优先级</th><th>今日 token</th><th>操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="ch in channels" :key="ch.id">
          <td>
            <span v-if="!ch.enabled" class="badge disabled">已禁用</span>
            <span v-else-if="ch.status === 'ok'" class="badge ok">正常</span>
            <span v-else-if="ch.status === 'cooling'" class="badge cooling">冷却至 {{ ch.coolingUntil }}</span>
            <span v-else-if="ch.status === 'auth-failed'" class="badge auth-failed" :title="ch.failReason">鉴权失败</span>
          </td>
          <td>{{ ch.name }}</td>
          <td class="muted">{{ ch.type }}</td>
          <td>{{ ch.models.join(', ') }}</td>
          <td>{{ ch.priority }}</td>
          <td>{{ ch.todayTokens }}</td>
          <td>
            <button @click="test(ch.id)">测试</button>
            <button @click="edit(ch)">编辑</button>
            <button class="danger" @click="remove(ch.id)">删除</button>
          </td>
        </tr>
        <tr v-if="channels.length === 0">
          <td colspan="7" class="muted">还没有渠道。点「添加渠道」从预设开始。</td>
        </tr>
      </tbody>
    </table>

    <div v-if="modalOpen" class="modal-mask" @click.self="modalOpen = false">
      <div class="modal">
        <h3>{{ form.ID ? '编辑渠道' : '添加渠道' }}</h3>
        <div v-if="!form.ID" class="field">
          <label>提供商预设</label>
          <select v-model="presetKey" @change="applyPreset">
            <option value="">— 手动配置 —</option>
            <option v-for="p in presets" :key="p.key" :value="p.key">{{ p.displayName }}</option>
          </select>
        </div>
        <div class="row">
          <div class="field">
            <label>名称</label>
            <input v-model="form.Name" placeholder="如 Kimi 套餐 A" />
          </div>
          <div class="field">
            <label>优先级（越大越优先）</label>
            <input v-model.number="form.Priority" type="number" />
          </div>
        </div>
        <div class="field">
          <label>Base URL</label>
          <input v-model="form.BaseURL" :placeholder="form.Type === 'grok' ? '留空使用官方端点' : 'https://...'" />
        </div>
        <div class="field" v-if="form.Type !== 'grok'">
          <label>API Key</label>
          <input v-model="form.APIKey" placeholder="sk-..." />
        </div>
        <div class="field">
          <label>对外模型名（逗号分隔，客户端请求用的名字）</label>
          <input v-model="modelsText" placeholder="claude-sonnet-4-6" />
        </div>
        <div class="field">
          <label>模型映射（对外名=上游名，每行一条，可留空）</label>
          <textarea
            v-model="mappingText"
            rows="3"
            style="width: 100%; background: var(--bg); color: var(--text); border: 1px solid var(--border); border-radius: 6px; padding: 6px 10px"
            placeholder="claude-sonnet-4-6=kimi-k3"
          />
        </div>
        <div class="field">
          <label><input type="checkbox" v-model="form.Enabled" style="width: auto" /> 启用</label>
        </div>
        <p v-if="formError" class="error-text">{{ formError }}</p>
        <div class="actions">
          <button class="primary" @click="save">保存</button>
          <button v-if="isGrok && !form.ID === false" @click="startOAuth">Grok 授权</button>
          <button @click="modalOpen = false">取消</button>
        </div>
        <p v-if="oauthInfo" class="muted">
          授权码 <b>{{ oauthInfo.userCode }}</b>，前往
          <a :href="oauthInfo.verificationUriComplete || oauthInfo.verificationUri" target="_blank" style="color: var(--accent)">验证页面</a>
          完成登录后点击「轮询授权结果」。
        </p>
        <div v-if="oauthInfo" class="actions">
          <button @click="pollOAuth">轮询授权结果</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { app, type ChannelView, type ChannelInput, type PresetView, type DeviceAuthInfo } from '../api'

const channels = ref<ChannelView[]>([])
const presets = ref<PresetView[]>([])
const modalOpen = ref(false)
const presetKey = ref('')
const modelsText = ref('')
const mappingText = ref('')
const formError = ref('')
const oauthInfo = ref<DeviceAuthInfo | null>(null)

const form = ref<ChannelInput>(emptyForm())

function emptyForm(): ChannelInput {
  return {
    ID: '', Name: '', Type: 'anthropic-compat', BaseURL: '', APIKey: '',
    Extra: null, Models: [], ModelMapping: null, Priority: 10, Enabled: true,
  }
}

const isGrok = computed(() => form.value.Type === 'grok')

async function refresh() {
  channels.value = await app().GetChannels()
}

onMounted(async () => {
  await refresh()
  presets.value = await app().GetPresets()
})

function openAdd() {
  form.value = emptyForm()
  presetKey.value = ''
  modelsText.value = ''
  mappingText.value = ''
  formError.value = ''
  oauthInfo.value = null
  modalOpen.value = true
}

function applyPreset() {
  const p = presets.value.find(x => x.key === presetKey.value)
  if (!p) return
  form.value.Type = p.type
  form.value.BaseURL = p.baseUrl
  modelsText.value = p.models.join(',')
  mappingText.value = p.mapping
    ? Object.entries(p.mapping).map(([k, v]) => `${k}=${v}`).join('\n')
    : ''
}

function edit(ch: ChannelView) {
  form.value = {
    ID: ch.id, Name: ch.name, Type: ch.type, BaseURL: ch.baseUrl, APIKey: '',
    Extra: null, Models: [...ch.models], ModelMapping: ch.modelMapping ? { ...ch.modelMapping } : null,
    Priority: ch.priority, Enabled: ch.enabled,
  }
  modelsText.value = ch.models.join(',')
  mappingText.value = ch.modelMapping
    ? Object.entries(ch.modelMapping).map(([k, v]) => `${k}=${v}`).join('\n')
    : ''
  formError.value = ''
  oauthInfo.value = null
  presetKey.value = ''
  modalOpen.value = true
}

async function save() {
  formError.value = ''
  form.value.Models = modelsText.value.split(',').map(s => s.trim()).filter(Boolean)
  const mapping: Record<string, string> = {}
  for (const line of mappingText.value.split('\n')) {
    const idx = line.indexOf('=')
    if (idx > 0) {
      const k = line.slice(0, idx).trim()
      const v = line.slice(idx + 1).trim()
      if (k && v) mapping[k] = v
    }
  }
  form.value.ModelMapping = Object.keys(mapping).length ? mapping : null
  try {
    await app().SaveChannel(form.value)
    modalOpen.value = false
    await refresh()
  } catch (e) {
    formError.value = String(e)
  }
}

async function remove(id: string) {
  await app().DeleteChannel(id)
  await refresh()
}

async function test(id: string) {
  try {
    await app().TestChannel(id)
    alert('连接正常')
  } catch (e) {
    alert('测试失败：' + String(e))
  }
}

async function startOAuth() {
  try {
    if (!form.value.ID) {
      formError.value = '请先保存渠道再发起授权'
      return
    }
    oauthInfo.value = await app().StartGrokDeviceAuth(form.value.ID)
  } catch (e) {
    formError.value = String(e)
  }
}

async function pollOAuth() {
  if (!oauthInfo.value) return
  try {
    await app().CompleteGrokDeviceAuth(form.value.ID, oauthInfo.value.deviceCode)
    oauthInfo.value = null
    alert('授权成功')
  } catch (e) {
    formError.value = String(e)
  }
}
</script>
