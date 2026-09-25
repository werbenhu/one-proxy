<template>
  <div>
    <h2>设置</h2>
    <div style="max-width: 480px">
      <div class="row">
        <div class="field">
          <label>监听地址</label>
          <input v-model="settings.listenHost" />
        </div>
        <div class="field">
          <label>端口</label>
          <input v-model.number="settings.listenPort" type="number" />
        </div>
      </div>
      <div class="field">
        <label>本地代理密钥（客户端 API Key）</label>
        <input v-model="settings.localKey" />
      </div>
      <div class="field">
        <label>日志保留天数</label>
        <input v-model.number="settings.retainDays" type="number" />
      </div>
      <div class="actions">
        <button class="primary" @click="save">保存</button>
      </div>
      <p v-if="saved" class="muted">已保存。监听地址修改后重启应用生效。</p>
      <p v-if="error" class="error-text">{{ error }}</p>

      <h3 style="margin-top: 32px">客户端接入</h3>
      <pre class="muted" style="white-space: pre-wrap; font-size: 12px">Anthropic（Claude Code）:
  ANTHROPIC_BASE_URL=http://{{ settings.listenHost }}:{{ settings.listenPort }}
  ANTHROPIC_API_KEY={{ settings.localKey }}

OpenAI Chat:
  OPENAI_BASE_URL=http://{{ settings.listenHost }}:{{ settings.listenPort }}/v1
  OPENAI_API_KEY={{ settings.localKey }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { app, type SettingsView } from '../api'

const settings = ref<SettingsView>({ listenHost: '127.0.0.1', listenPort: 8280, localKey: '', retainDays: 90 })
const saved = ref(false)
const error = ref('')

onMounted(async () => {
  settings.value = await app().GetSettings()
})

async function save() {
  error.value = ''
  saved.value = false
  try {
    await app().SaveSettings(settings.value)
    saved.value = true
  } catch (e) {
    error.value = String(e)
  }
}
</script>
