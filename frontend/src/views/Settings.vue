<template>
  <div class="settings-page">
    <div class="page-head">
      <div>
        <h2>{{ t('nav.settings') }}</h2>
      </div>
    </div>

    <div class="settings-grid">
      <section class="card base-card">
        <div class="card-head">
          <h3 class="card-title">{{ t('settings.base') }}</h3>
          <div class="card-head-actions">
            <span v-if="saved" class="save-ok">{{ t('settings.saved') }}</span>
            <button class="primary" @click="save">{{ t('common.save') }}</button>
          </div>
        </div>
        <div class="base-fields">
          <div class="field">
            <label>{{ t('settings.listenHost') }}</label>
            <input v-model="settings.listenHost" />
          </div>
          <div class="field">
            <label>{{ t('settings.listenPort') }}</label>
            <input v-model.number="settings.listenPort" type="number" />
          </div>
          <div class="field">
            <label>{{ t('settings.retainDays') }}</label>
            <input v-model.number="settings.retainDays" type="number" min="1" />
          </div>
          <div class="field span-2">
            <label>{{ t('settings.localKey') }}</label>
            <div class="key-field">
              <input v-model="settings.localKey" :type="showKey ? 'text' : 'password'" />
              <button type="button" class="icon-btn" :title="showKey ? t('common.hide') : t('common.show')" @click="showKey = !showKey">
                {{ showKey ? t('common.hide') : t('common.show') }}
              </button>
            </div>
          </div>
          <div class="field">
            <label>{{ t('settings.theme') }}</label>
            <div class="segmented">
              <button type="button" :class="{ on: settings.theme !== 'light' }" @click="previewTheme('dark')">{{ t('settings.themeDark') }}</button>
              <button type="button" :class="{ on: settings.theme === 'light' }" @click="previewTheme('light')">{{ t('settings.themeLight') }}</button>
            </div>
          </div>
          <div class="field span-2">
            <label>{{ t('settings.globalProxy') }}</label>
            <input v-model="settings.globalProxy" :placeholder="t('settings.globalProxyPlaceholder')" />
            <p class="field-hint">{{ t('settings.globalProxyHint') }}</p>
          </div>
          <div class="field">
            <label>{{ t('settings.language') }}</label>
            <div class="segmented">
              <button type="button" :class="{ on: settings.language !== 'en' }" @click="previewLocale('zh')">中文</button>
              <button type="button" :class="{ on: settings.language === 'en' }" @click="previewLocale('en')">English</button>
            </div>
          </div>
        </div>
        <p v-if="error" class="error-text">{{ error }}</p>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { app, type SettingsView } from '../api'
import { applyLocale, applyTheme, locale, t, theme } from '../i18n'

const settings = ref<SettingsView>({ listenHost: '127.0.0.1', listenPort: 8280, localKey: '', retainDays: 90, theme: 'light', language: 'zh', globalProxy: '' })
const saved = ref(false)
const error = ref('')
const showKey = ref(false)

onMounted(async () => {
  const loaded = await app().GetSettings()
  // 主题/语言以当前实际生效值为准：进入页面前可能刚在别处预览过，
  // 后端快照里仍是未保存的旧值，不能直接覆盖。
  settings.value = { ...loaded, theme: theme.value, language: locale.value }
})

function previewTheme(value: string) {
  settings.value.theme = value
  applyTheme(value)
}
function previewLocale(value: string) {
  settings.value.language = value
  applyLocale(value)
}

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

.card-title {
  margin: 0 0 18px;
  font-size: 15px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border);
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 18px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border);
}

.card-head .card-title {
  margin: 0;
  padding-bottom: 0;
  border-bottom: 0;
}

.card-head-actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.save-ok {
  color: var(--accent);
  font-size: 13px;
}

.field-hint {
  margin: 6px 0 0;
  font-size: 12px;
  color: var(--muted);
}

.base-card {
  grid-column: 1 / -1;
}

.base-fields {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 14px 16px;
  align-items: start;
}

.base-fields .span-2 {
  grid-column: span 2;
}

.base-fields .field {
  margin: 0;
}

.base-fields .segmented {
  display: flex;
  width: 100%;
}

.base-fields .segmented button {
  flex: 1;
  padding: 6px 12px;
}

@media (max-width: 560px) {
  .settings-grid,
  .base-fields {
    grid-template-columns: 1fr;
  }

  .base-fields .span-2 {
    grid-column: auto;
  }
}
</style>
