<template>
  <div>
    <div v-if="colored.length" class="trend-legend">
      <span v-for="s in colored" :key="s.name"><i :style="{ background: s.color }"></i>{{ s.name }}</span>
    </div>
    <svg class="trend-chart" :viewBox="'0 0 ' + W + ' ' + H">
      <line v-for="y in gridYs" :key="y" class="trend-grid" :x1="PAD.l" :x2="W - PAD.r" :y1="y" :y2="y" />
      <path v-for="s in colored" :key="'line-' + s.name" :d="pathOf(s.values)" fill="none" :stroke="s.color" stroke-width="2" stroke-linecap="round" />
      <g v-for="s in colored" :key="'pts-' + s.name">
        <template v-for="(v, i) in s.values" :key="i">
          <circle v-if="showDots" :cx="xAt(i)" :cy="yAt(v)" r="1.8" :fill="s.color" />
          <circle :cx="xAt(i)" :cy="yAt(v)" r="5" fill="transparent">
            <title>{{ s.name }} · {{ dayLabel(days[i]) }} · {{ formatNumber(v) }}</title>
          </circle>
        </template>
      </g>
      <text v-for="l in xLabels" :key="l.i" :x="xAt(l.i)" :y="H - 6" text-anchor="middle">{{ l.text }}</text>
    </svg>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { monthDayLabel } from '../i18n'

const props = defineProps<{ days: string[]; series: { name: string; values: number[] }[] }>()

const PALETTE = ['#4c8dff', '#35c9a0', '#b57bff', '#e06c5f', '#e0b341', '#4cc3d9', '#ff7eb6', '#9acd32']
const W = 660
const H = 190
const PAD = { l: 10, r: 10, t: 12, b: 24 }
const gridYs = [PAD.t, (PAD.t + H - PAD.b) / 2, H - PAD.b]

const colored = computed(() => props.series.map((s, i) => ({ ...s, color: PALETTE[i % PALETTE.length] })))
const maxY = computed(() => Math.max(0, ...props.series.flatMap(s => s.values)))
const showDots = computed(() => props.days.length <= 10)

function xAt(i: number): number {
  const n = props.days.length
  return PAD.l + (n <= 1 ? 0 : (i * (W - PAD.l - PAD.r)) / (n - 1))
}
function yAt(v: number): number {
  const m = maxY.value
  return H - PAD.b - (m > 0 ? (v / m) * (H - PAD.t - PAD.b) : 0)
}
// Catmull-Rom 转三次贝塞尔的平滑折线。
function pathOf(values: number[]): string {
  const n = values.length
  if (!n) return ''
  const pts = values.map((v, i) => ({ x: xAt(i), y: yAt(v) }))
  if (n === 1) return 'M ' + pts[0].x + ' ' + pts[0].y
  let d = 'M ' + pts[0].x + ' ' + pts[0].y
  for (let i = 0; i < n - 1; i++) {
    const p0 = pts[Math.max(0, i - 1)], p1 = pts[i], p2 = pts[i + 1], p3 = pts[Math.min(n - 1, i + 2)]
    const c1x = p1.x + (p2.x - p0.x) / 6, c1y = p1.y + (p2.y - p0.y) / 6
    const c2x = p2.x - (p3.x - p1.x) / 6, c2y = p2.y - (p3.y - p1.y) / 6
    d += ' C ' + c1x + ' ' + c1y + ' ' + c2x + ' ' + c2y + ' ' + p2.x + ' ' + p2.y
  }
  return d
}
const xLabels = computed(() => {
  const n = props.days.length
  return props.days
    .map((d, i) => ({ i, text: dayLabel(d) }))
    .filter(({ i }) => n <= 10 || i % 5 === 0 || i === n - 1)
})
function dayLabel(key: string): string { return monthDayLabel(Number(key.slice(5, 7)), Number(key.slice(8, 10))) }
function formatNumber(value: number) { return new Intl.NumberFormat('zh-CN').format(value) }
</script>
