import { createApp } from 'vue'
import { createRouter, createWebHashHistory } from 'vue-router'
import App from './App.vue'
import Channels from './views/Channels.vue'
import Usage from './views/Usage.vue'
import Settings from './views/Settings.vue'
import './style.css'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/channels' },
    { path: '/channels', component: Channels },
    { path: '/usage', component: Usage },
    { path: '/settings', component: Settings },
  ],
})

createApp(App).use(router).mount('#app')
