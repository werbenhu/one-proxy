<template>
  <div>
    <h2>用量</h2>
    <div class="actions">
      <select v-model="rangeKey" style="width: 160px" @change="refresh">
        <option value="today">今天</option>
        <option value="7d">最近 7 天</option>
        <option value="30d">最近 30 天</option>
        <option value="all">全部</option>
      </select>
      <button @click="refresh">刷新</button>
    </div>
    <table>
      <thead>
        <tr>
          <th>提供商账户</th><th>对外模型</th><th>上游模型</th><th>请求数</th>
          <th>输入 token</th><th>输出 token</th><th>缓存读</th><th>错误</th>
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
          <td colspan="8" class="muted">暂无数据（发几个请求后刷新）</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { app, type AggRow } from '../api'

const rows = ref<AggRow[]>([])
const rangeKey = ref('today')

async function refresh() {
  rows.value = await app().GetUsageSummary(rangeKey.value)
}

onMounted(refresh)
</script>
