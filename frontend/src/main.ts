import { createApp } from 'vue'
import { createRouter, createWebHashHistory } from 'vue-router'
import App from './App.vue'
import Channels from './views/Channels.vue'
import Providers from './views/Providers.vue'
import Usage from './views/Usage.vue'
import Clients from './views/Clients.vue'
import Settings from './views/Settings.vue'
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

createApp(App).use(router).mount('#app')
