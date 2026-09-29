import { createApp } from 'vue'
import { createRouter, createWebHashHistory } from 'vue-router'
import App from './App.vue'
import Channels from './views/Channels.vue'
import Providers from './views/Providers.vue'
import Usage from './views/Usage.vue'
import Clients from './views/Clients.vue'
import Settings from './views/Settings.vue'
import { toast } from './ui'
import { t } from './i18n'
import './style.css'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/providers' },
    { path: '/providers', component: Providers },
    { path: '/channels', component: Channels },
    { path: '/usage', component: Usage },
    { path: '/clients', component: Clients },
    { path: '/settings', component: Settings },
  ],
})

// 全局错误兜底：任何未捕获异常以 toast 呈现，避免界面静默白屏后无从排查。
window.addEventListener('error', e => {
  toast(t('common.uiError') + (e.message || 'unknown error'), 'error', 8000)
})
window.addEventListener('unhandledrejection', e => {
  const reason: unknown = e.reason
  const msg = reason instanceof Error ? reason.message : String(reason ?? 'unknown')
  toast(t('common.callFailed') + msg, 'error', 8000)
})

// 绑定通道心跳：睡眠唤醒后 WebView2 渲染器可能冻住（绑定调用挂起、页面不再更新）。
// 定时 Ping 后端，连续超时即 location.reload() 重建渲染器——代理进程不受影响，秒级恢复。
;(() => {
  if (!window.go?.main?.App?.Ping) return
  const INTERVAL = 10_000
  const TIMEOUT = 5_000
  const MAX_FAILS = 3
  let fails = 0
  let reloading = false
  function beat() {
    if (reloading) return
    let settled = false
    const timer = setTimeout(() => {
      if (settled) return
      settled = true
      if (++fails >= MAX_FAILS) {
        reloading = true
        location.reload()
      }
    }, TIMEOUT)
    window.go.main.App.Ping().then(
      () => { if (!settled) { settled = true; fails = 0 } },
      () => { if (!settled) { settled = true; fails = 0 } }, // 立即拒绝说明通道还活着，不累计失败
    ).catch(() => { clearTimeout(timer) })
  }
  setInterval(beat, INTERVAL)
  document.addEventListener('visibilitychange', beat)
})()

createApp(App).use(router).mount('#app')
