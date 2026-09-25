<template>
  <div class="shell">
    <nav class="nav">
      <div class="brand"><img src="./assets/oneproxy-mark.svg" alt="" /> <span>OneProxy</span></div>
      <router-link to="/providers">{{ t('nav.providers') }}</router-link>
      <router-link to="/channels">{{ t('nav.channels') }}</router-link>
      <router-link to="/usage">{{ t('nav.usage') }}</router-link>
      <router-link to="/settings">{{ t('nav.settings') }}</router-link>
    </nav>
    <main class="main">
      <router-view />
    </main>

    <div class="toast-stack">
      <div v-for="item in toasts" :key="item.id" class="toast" :class="item.kind">{{ item.text }}</div>
    </div>

    <div v-if="confirmBox" class="modal-mask" @click.self="settleConfirm(false)">
      <div class="modal modal-small">
        <h3>{{ t('common.confirmTitle') }}</h3>
        <p class="confirm-text">{{ confirmBox.text }}</p>
        <div class="actions modal-actions">
          <button class="danger" @click="settleConfirm(true)">{{ t('common.confirm') }}</button>
          <button @click="settleConfirm(false)">{{ t('common.cancel') }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { app } from './api'
import { initUI, t } from './i18n'
import { confirmBox, settleConfirm, toasts } from './ui'

onMounted(async () => {
  initUI(await app().GetSettings())
})
</script>
