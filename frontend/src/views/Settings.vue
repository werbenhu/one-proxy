<template>
  <div class="settings-page">
    <div class="page-head">
      <div>
        <h2>{{ t('nav.settings') }}</h2>
      </div>
    </div>

    <div class="settings-grid">
      <section class="card card-wide">
        <h3 class="card-title">{{ t('settings.interface') }}</h3>
        <div class="row">
          <div class="field">
            <label>{{ t('settings.theme') }}</label>
            <div class="segmented">
              <button type="button" :class="{ on: settings.theme !== 'light' }" @click="previewTheme('dark')">{{ t('settings.themeDark') }}</button>
              <button type="button" :class="{ on: settings.theme === 'light' }" @click="previewTheme('light')">{{ t('settings.themeLight') }}</button>
            </div>
          </div>
          <div class="field">
            <label>{{ t('settings.language') }}</label>
            <div class="segmented">
              <button type="button" :class="{ on: settings.language !== 'en' }" @click="previewLocale('zh')">中文</button>
              <button type="button" :class="{ on: settings.language === 'en' }" @click="previewLocale('en')">English</button>
            </div>
          </div>
        </div>
      </section>

      <section class="card">
        <h3 class="card-title">{{ t('settings.base') }}</h3>
        <div class="row">
          <div class="field">
            <label>{{ t('settings.listenHost') }}</label>
            <input v-model="settings.listenHost" />
          </div>
          <div class="field">
            <label>{{ t('settings.listenPort') }}</label>
            <input v-model.number="settings.listenPort" type="number" />
          </div>
        </div>
        <div class="field">
          <label>{{ t('settings.localKey') }}</label>
          <div class="key-field">
            <input v-model="settings.localKey" :type="showKey ? 'text' : 'password'" />
            <button type="button" class="icon-btn" :title="showKey ? t('common.hide') : t('common.show')" @click="showKey = !showKey">
              {{ showKey ? t('common.hide') : t('common.show') }}
            </button>
          </div>
        </div>
        <div class="field">
          <label>{{ t('settings.retainDays') }}</label>
          <input v-model.number="settings.retainDays" type="number" min="1" />
        </div>
        <div class="actions card-actions">
          <button class="primary" @click="save">{{ t('common.save') }}</button>
          <span v-if="saved" class="save-ok">{{ t('settings.saved') }}</span>
        </div>
        <p v-if="error" class="error-text">{{ error }}</p>
      </section>

      <section class="card">
        <h3 class="card-title">{{ t('settings.clients') }}</h3>
        <div v-for="client in clients" :key="client.name" class="client-block">
          <div class="client-head">
            <span class="client-name">{{ client.name }}</span>
            <button type="button" class="icon-btn" @click="copy(client)">
              {{ copied === client.name ? t('common.copied') : t('common.copy') }}
            </button>
          </div>
          <pre class="client-env">{{ client.env }}</pre>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { app, type SettingsView } from '../api'
import { applyLocale, applyTheme, t } from '../i18n'

const settings = ref<SettingsView>({ listenHost: '127.0.0.1', listenPort: 8280, localKey: '', retainDays: 90, theme: 'dark', language: 'zh' })
const saved = ref(false)
const error = ref('')
const showKey = ref(false)
const copied = ref('')

onMounted(async () => {
  settings.value = await app().GetSettings()
})

function previewTheme(value: string) {
  settings.value.theme = value
  applyTheme(value)
}
function previewLocale(value: string) {
  settings.value.language = value
  applyLocale(value)
}

const clients = computed(() => {
  const { listenHost, listenPort, localKey } = settings.value
  const base = `http://${listenHost}:${listenPort}`
  return [
    {
      name: 'Anthropic（Claude Code）',
      env: `ANTHROPIC_BASE_URL=${base}\nANTHROPIC_API_KEY=${localKey}`,
    },
    {
      name: 'OpenAI Chat',
      env: `OPENAI_BASE_URL=${base}/v1\nOPENAI_API_KEY=${localKey}`,
    },
    {
      name: 'OpenAI Responses',
      env: `OPENAI_BASE_URL=${base}/v1\nOPENAI_API_KEY=${localKey}\n# ${t('settings.responsesPath')}`,
    },
  ]
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

async function copy(client: { name: string; env: string }) {
  try {
    await navigator.clipboard.writeText(client.env)
    copied.value = client.name
    setTimeout(() => { if (copied.value === client.name) copied.value = '' }, 1500)
  } catch (e) {
    error.value = String(e)
  }
}
</script>

<style scoped>
.settings-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(360px, 1fr));
  gap: 16px;
  align-items: start;
  max-width: 1100px;
}

.card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 20px;
}

.card-wide {
  grid-column: 1 / -1;
}

.card-title {
  margin: 0 0 18px;
  font-size: 15px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border);
}

.card-actions {
  align-items: center;
  margin: 18px 0 0;
}

.save-ok {
  color: var(--accent);
  font-size: 13px;
}

.client-block + .client-block {
  margin-top: 16px;
}

.client-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.client-name {
  font-weight: 600;
  font-size: 13px;
}

.client-env {
  margin: 0;
  padding: 12px;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  color: var(--code);
  font-family: "Cascadia Code", Consolas, monospace;
  font-size: 12px;
  line-height: 1.7;
  white-space: pre-wrap;
  word-break: break-all;
}

@media (max-width: 560px) {
  .settings-grid {
    grid-template-columns: 1fr;
  }
}
</style>
