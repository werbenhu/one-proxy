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
          <td>{{ formatNumber(r.requests) }}</td>
          <td>{{ formatNumber(r.inputTokens) }}</td>
          <td>{{ formatNumber(r.outputTokens) }}</td>
          <td>{{ formatNumber(r.cacheReadTokens) }}</td>
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
import { app, type AggRow, type ModelDayTokens, type ModelHourTokens } from '../api'
import { t, formatNumber } from '../i18n'
import TrendChart from '../components/TrendChart.vue'

const rows = ref<AggRow[]>([])
const daily = ref<ModelDayTokens[]>([])
const hourly = ref<ModelHourTokens[]>([])
const rangeKey = ref('today')

async function refresh() {
  const [summary, trend, trendHourly] = await Promise.all([
    app().GetUsageSummary(rangeKey.value),
    app().GetUsageDaily(rangeKey.value),
    app().GetUsageHourlyToday(),
  ])
  rows.value = summary
  daily.value = trend
  hourly.value = trendHourly
}

onMounted(refresh)

function dayKey(d: Date): string {
  return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0')
}

// 今日：x 轴为当前小时往前 12 小时；其余范围：x 轴为自然日。
const chartDays = computed(() => {
  if (rangeKey.value === 'today') {
    const nowHour = new Date()
    const keys: string[] = []
    for (let i = 12; i >= 0; i--) {
      const d = new Date(nowHour); d.setHours(d.getHours() - i, 0, 0, 0)
      const day = dayKey(d)
      const hour = String(d.getHours()).padStart(2, '0')
      keys.push(day + 'T' + hour)
    }
    return keys
  }
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
  if (rangeKey.value === 'today') {
    for (const r of hourly.value) {
      // x 轴 key 与后端 bucket（"YYYY-MM-DDTHH"）一致，跨零点也能对上。
      let m = byModel.get(r.model)
      if (!m) { m = new Map(); byModel.set(r.model, m) }
      m.set(r.bucket, (m.get(r.bucket) ?? 0) + r.tokens)
    }
  } else {
    for (const r of daily.value) {
      let m = byModel.get(r.model)
      if (!m) { m = new Map(); byModel.set(r.model, m) }
      m.set(r.day, (m.get(r.day) ?? 0) + r.tokens)
    }
  }
  return [...byModel.entries()]
    .map(([name, m]) => ({ name, values: chartDays.value.map(d => m.get(d) ?? 0), total: chartDays.value.reduce((a, d) => a + (m.get(d) ?? 0), 0) }))
    .filter(s => s.total > 0)
    .sort((a, b) => b.total - a.total)
    .map(({ name, values }) => ({ name, values }))
})
</script>
