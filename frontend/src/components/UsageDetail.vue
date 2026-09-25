<template>
  <div class="modal-mask" @click.self="emit('close')">
    <div class="modal modal-wide">
      <h3>{{ provider.name }} · {{ t('usageDetail.title') }}</h3>
      <p v-if="loading" class="muted">{{ t('common.loading') }}</p>
      <template v-else>
        <div class="usage-summary">
          <div class="usage-chip"><span>{{ t('usageDetail.today') }}</span><b>{{ formatNumber(usage?.todayTokens ?? 0) }}</b></div>
          <div class="usage-chip"><span>{{ t('usageDetail.thisWeek') }}</span><b>{{ formatNumber(usage?.weekTokens ?? 0) }}</b></div>
          <div class="usage-chip"><span>{{ t('usageDetail.thisMonth') }}</span><b>{{ formatNumber(usage?.monthTokens ?? 0) }}</b></div>
          <div class="usage-chip"><span>{{ t('usageDetail.total') }}</span><b>{{ formatNumber(usage?.totalTokens ?? 0) }}</b></div>
        </div>
        <p v-if="empty" class="muted">{{ t('usageDetail.empty') }}</p>
        <template v-else>
          <div v-if="usage?.models?.length" class="usage-section">
            <div class="usage-head"><h4>{{ t('usageDetail.byModel') }}</h4></div>
            <table class="model-stat-table">
              <thead>
                <tr>
                  <th>{{ t('usageDetail.model') }}</th><th class="num">{{ t('usage.requests') }}</th><th class="num">{{ t('usage.inputTokens') }}</th>
                  <th class="num">{{ t('usage.outputTokens') }}</th><th class="num">{{ t('usage.cacheRead') }}</th><th class="num">{{ t('usageDetail.totalTokens') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="m in usage.models" :key="m.model">
                  <td>{{ m.model }}</td>
                  <td class="num">{{ formatNumber(m.requests) }}</td>
                  <td class="num">{{ formatNumber(m.inputTokens) }}</td>
                  <td class="num">{{ formatNumber(m.outputTokens) }}</td>
                  <td class="num">{{ formatNumber(m.cacheReadTokens) }}</td>
                  <td class="num"><b>{{ formatNumber(m.totalTokens) }}</b></td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="usage-section">
            <div class="usage-head">
              <h4>{{ t('usageDetail.activity') }}</h4>
              <div class="segmented">
                <button :class="{ on: mode === 'day' }" @click="mode = 'day'">{{ t('usageDetail.daily') }}</button>
                <button :class="{ on: mode === 'week' }" @click="mode = 'week'">{{ t('usageDetail.weekly') }}</button>
                <button :class="{ on: mode === 'cumulative' }" @click="mode = 'cumulative'">{{ t('usageDetail.cumulative') }}</button>
              </div>
            </div>
            <div class="heatmap-scroll">
              <div v-if="mode !== 'week'" class="heatmap">
                <div v-for="(col, i) in heatCells" :key="i" class="heatmap-col">
                  <div v-for="(cell, j) in col" :key="j" class="heatmap-cell" :class="cell.date ? 'hm-' + cell.level : 'hm-blank'"
                    :title="cell.date ? fmtDay(cell.date) + t('usageDetail.colon') + formatNumber(cell.value) + ' ' + t('common.tokens') : ''"></div>
                </div>
              </div>
              <div v-else class="heatmap-week">
                <div v-for="(b, i) in weekBlocks" :key="i" class="heatmap-cell" :class="'hm-' + b.level" :title="b.tip"></div>
              </div>
              <div class="heatmap-months"><span v-for="m in monthLabels" :key="m.col" :style="{ left: m.col * 14 + 'px' }">{{ m.text }}</span></div>
            </div>
          </div>
          <div class="usage-section">
            <div class="usage-head">
              <h4>{{ t('usageDetail.trend') }}</h4>
              <div class="segmented">
                <button :class="{ on: range === 7 }" @click="range = 7">{{ t('usageDetail.last7Days') }}</button>
                <button :class="{ on: range === 30 }" @click="range = 30">{{ t('usageDetail.last30Days') }}</button>
              </div>
            </div>
            <TrendChart :days="trendDays" :series="trendSeries" />
          </div>
        </template>
      </template>
      <div class="actions modal-actions"><button @click="emit('close')">{{ t('common.close') }}</button></div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { app, type ProviderUsageView, type ProviderView } from '../api'
import { t, monthDayLabel, monthName } from '../i18n'
import { toast } from '../ui'
import TrendChart from './TrendChart.vue'

const props = defineProps<{ provider: ProviderView }>()
const emit = defineEmits<{ close: [] }>()

const usage = ref<ProviderUsageView | null>(null)
const loading = ref(true)
const mode = ref<'day' | 'week' | 'cumulative'>('day')
const range = ref<7 | 30>(7)

onMounted(async () => {
  try {
    usage.value = await app().GetProviderUsage(props.provider.id)
  } catch (e) {
    toast(t('usageDetail.loadFailed') + String(e), 'error', 5000)
    emit('close')
  } finally {
    loading.value = false
  }
})

const empty = computed(() => !(usage.value?.daily?.length))

function dayKey(d: Date): string {
  return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0')
}
function fmtDay(d: Date): string { return monthDayLabel(d.getMonth() + 1, d.getDate()) }
function formatNumber(value: number) { return new Intl.NumberFormat('zh-CN').format(value) }
function levelOf(v: number, max: number): number {
  if (v <= 0 || max <= 0) return 0
  return Math.min(4, Math.max(1, Math.ceil((v / max) * 4)))
}

interface HeatCell { key: string; date: Date | null; value: number }

// 近一年按周列 ×  weekday 行（周一起）的原始逐日格子。
const heatColumns = computed<HeatCell[][]>(() => {
  const today = new Date(); today.setHours(0, 0, 0, 0)
  const start = new Date(today)
  start.setDate(start.getDate() - 364)
  start.setDate(start.getDate() - ((start.getDay() + 6) % 7))
  const byDay = new Map<string, number>()
  for (const r of usage.value?.daily ?? []) byDay.set(r.day, r.tokens)
  const cols: HeatCell[][] = []
  const cursor = new Date(start)
  while (cursor <= today) {
    const col: HeatCell[] = []
    for (let d = 0; d < 7; d++) {
      if (cursor > today) {
        col.push({ key: '', date: null, value: 0 })
      } else {
        const key = dayKey(cursor)
        col.push({ key, date: new Date(cursor), value: byDay.get(key) ?? 0 })
      }
      cursor.setDate(cursor.getDate() + 1)
    }
    cols.push(col)
  }
  return cols
})

// 每日 / 累计模式的格子（含色阶）。
const heatCells = computed(() => {
  let run = 0
  if (mode.value === 'cumulative') {
    const firstKey = heatColumns.value[0]?.find(c => c.key)?.key ?? ''
    for (const r of usage.value?.daily ?? []) if (r.day < firstKey) run += r.tokens
  }
  const cells = heatColumns.value.map(col => col.map(c => {
    if (!c.date) return { date: null as Date | null, value: 0 }
    run += c.value
    return { date: c.date, value: mode.value === 'cumulative' ? run : c.value }
  }))
  const max = Math.max(0, ...cells.flat().map(c => c.value))
  return cells.map(col => col.map(c => ({ ...c, level: levelOf(c.value, max) })))
})

// 每周模式：一列一个块，值 = 该周合计。
const weekBlocks = computed(() => {
  const blocks = heatColumns.value.map(col => {
    const dates = col.filter(c => c.date).map(c => c.date!)
    return { first: dates[0] ?? null, last: dates[dates.length - 1] ?? null, value: col.reduce((a, c) => a + c.value, 0) }
  })
  const max = Math.max(0, ...blocks.map(b => b.value))
  return blocks.map(b => ({
    level: levelOf(b.value, max),
    tip: b.first && b.last ? fmtDay(b.first) + ' ~ ' + fmtDay(b.last) + t('usageDetail.colon') + formatNumber(b.value) + ' ' + t('common.tokens') : '',
  }))
})

const monthLabels = computed(() => {
  const labels: { text: string; col: number }[] = []
  let prev = -1
  heatColumns.value.forEach((col, i) => {
    const first = col.find(c => c.date)?.date
    if (!first) return
    const m = first.getMonth()
    if (m !== prev) { labels.push({ text: monthName(m + 1), col: i }); prev = m }
  })
  return labels
})

const trendDays = computed(() => {
  const days: string[] = []
  const today = new Date(); today.setHours(0, 0, 0, 0)
  for (let i = range.value - 1; i >= 0; i--) {
    const d = new Date(today); d.setDate(d.getDate() - i)
    days.push(dayKey(d))
  }
  return days
})

const trendSeries = computed(() => {
  const byModel = new Map<string, Map<string, number>>()
  for (const r of usage.value?.modelDaily ?? []) {
    let m = byModel.get(r.model)
    if (!m) { m = new Map(); byModel.set(r.model, m) }
    m.set(r.day, (m.get(r.day) ?? 0) + r.tokens)
  }
  return [...byModel.entries()]
    .map(([name, m]) => ({ name, values: trendDays.value.map(d => m.get(d) ?? 0), total: trendDays.value.reduce((a, d) => a + (m.get(d) ?? 0), 0) }))
    .filter(s => s.total > 0)
    .sort((a, b) => b.total - a.total)
    .map(({ name, values }) => ({ name, values }))
})
</script>
