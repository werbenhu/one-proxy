<template>
  <div>
    <h2>{{ t('nav.usage') }}</h2>
    <div class="actions">
      <select v-model="rangeKey" style="width: 160px" @change="refresh">
        <option value="today">{{ t('usage.today') }}</option>
        <option value="7d">{{ t('usage.last7Days') }}</option>
        <option value="30d">{{ t('usage.last30Days') }}</option>
        <option value="all">{{ t('usage.all') }}</option>
      </select>
      <button @click="refresh">{{ t('common.refresh') }}</button>
    </div>
    <div v-if="chartSeries.length" class="usage-chart">
      <TrendChart :days="chartDays" :series="chartSeries" />
    </div>
    <table>
      <thead>
        <tr>
          <th>{{ t('usage.providerAccount') }}</th><th>{{ t('usage.publicModel') }}</th><th>{{ t('usage.upstreamModel') }}</th><th>{{ t('usage.requests') }}</th>
          <th>{{ t('usage.inputTokens') }}</th><th>{{ t('usage.outputTokens') }}</th><th>{{ t('usage.cacheRead') }}</th><th>{{ t('usage.errors') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(r, i) in rows" :key="i">
          <td>{{ r.channelName }}</td>
          <td>{{ r.modelRequested }}</td>
          <td class="muted">{{ r.modelUpstream }}</td>
          <td>{{ r.requests }}</td>
          <td>{{ r.inputTokens }}</td>
          <td>{{ r.outputTokens }}</td>
          <td>{{ r.cacheReadTokens }}</td>
          <td>{{ r.errors || '' }}</td>
        </tr>
        <tr v-if="rows.length === 0">
          <td colspan="8" class="muted">{{ t('usage.emptyHint') }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { app, type AggRow, type ModelDayTokens } from '../api'
import { t } from '../i18n'
import TrendChart from '../components/TrendChart.vue'

const rows = ref<AggRow[]>([])
const daily = ref<ModelDayTokens[]>([])
const rangeKey = ref('today')

async function refresh() {
  const [summary, trend] = await Promise.all([
    app().GetUsageSummary(rangeKey.value),
    app().GetUsageDaily(rangeKey.value),
  ])
  rows.value = summary
  daily.value = trend
}

onMounted(refresh)

function dayKey(d: Date): string {
  return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0')
}

const chartDays = computed(() => {
  const today = new Date(); today.setHours(0, 0, 0, 0)
  let n = 1
  if (rangeKey.value === '7d') n = 7
  else if (rangeKey.value === '30d') n = 30
  else if (rangeKey.value === 'all') {
    const first = daily.value[0]?.day
    if (first) {
      const start = new Date(first + 'T00:00:00')
      n = Math.min(370, Math.max(1, Math.round((today.getTime() - start.getTime()) / 86400000) + 1))
    } else {
      n = 30
    }
  }
  const keys: string[] = []
  for (let i = n - 1; i >= 0; i--) {
    const d = new Date(today); d.setDate(d.getDate() - i)
    keys.push(dayKey(d))
  }
  return keys
})

const chartSeries = computed(() => {
  const byModel = new Map<string, Map<string, number>>()
  for (const r of daily.value) {
    let m = byModel.get(r.model)
    if (!m) { m = new Map(); byModel.set(r.model, m) }
    m.set(r.day, (m.get(r.day) ?? 0) + r.tokens)
  }
  return [...byModel.entries()]
    .map(([name, m]) => ({ name, values: chartDays.value.map(d => m.get(d) ?? 0), total: chartDays.value.reduce((a, d) => a + (m.get(d) ?? 0), 0) }))
    .filter(s => s.total > 0)
    .sort((a, b) => b.total - a.total)
    .map(({ name, values }) => ({ name, values }))
})
</script>
