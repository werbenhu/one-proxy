<template>
  <div class="clients-page">
    <div class="page-head">
      <div>
        <h2>{{ t('settings.clients') }}</h2>
      </div>
    </div>

    <section class="card">
      <p class="page-hint">{{ t('clients.hint') }}</p>
      <div class="client-list">
        <div v-for="client in clients" :key="client.name" class="client-block">
          <div class="client-head">
            <span class="client-name">{{ client.name }}</span>
            <button type="button" class="icon-btn" @click="copy(client)">
              {{ copied === client.name ? t('common.copied') : t('common.copy') }}
            </button>
          </div>
          <pre class="client-env">{{ client.env }}</pre>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { app } from '../api'
import { t } from '../i18n'

const listenHost = ref('127.0.0.1')
const listenPort = ref(8280)
const localKey = ref('')
const copied = ref('')
const error = ref('')

onMounted(async () => {
  const s = await app().GetSettings()
  listenHost.value = s.listenHost
  listenPort.value = s.listenPort
  localKey.value = s.localKey
})

const clients = computed(() => {
  const base = `http://${listenHost.value}:${listenPort.value}`
  const cid = t('settings.channelIdPlaceholder')
  return [
    {
      name: 'Anthropic（Claude Code）',
      env: `ANTHROPIC_BASE_URL=${base}/${cid}\nANTHROPIC_API_KEY=${localKey.value}`,
    },
    {
      name: 'OpenAI（Chat / Responses）',
      env: `OPENAI_BASE_URL=${base}/${cid}/v1\nOPENAI_API_KEY=${localKey.value}\n# ${t('settings.openaiEndpoints')}`,
    },
  ]
})

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
.card {
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 20px;
  max-width: 1100px;
}

.page-hint {
  margin: 0 0 16px;
  font-size: 13px;
  color: var(--muted);
}

.client-list {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(420px, 1fr));
  gap: 16px;
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
  white-space: pre;
  overflow-x: auto;
}

@media (max-width: 560px) {
  .client-list {
    grid-template-columns: 1fr;
  }
}
</style>
