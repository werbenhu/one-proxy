<template>
  <div class="shell">
    <nav class="nav">
      <div class="brand"><img src="./assets/oneproxy-mark.svg" alt="" /> <span>OneProxy</span></div>
      <router-link to="/providers">提供商</router-link>
      <router-link to="/channels">渠道</router-link>
      <router-link to="/usage">用量</router-link>
      <router-link to="/settings">设置</router-link>
    </nav>
    <main class="main">
      <router-view />
    </main>

    <div class="toast-stack">
      <div v-for="item in toasts" :key="item.id" class="toast" :class="item.kind">{{ item.text }}</div>
    </div>

    <div v-if="confirmBox" class="modal-mask" @click.self="settleConfirm(false)">
      <div class="modal modal-small">
        <h3>确认操作</h3>
        <p class="confirm-text">{{ confirmBox.text }}</p>
        <div class="actions modal-actions">
          <button class="danger" @click="settleConfirm(true)">确认</button>
          <button @click="settleConfirm(false)">取消</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { confirmBox, settleConfirm, toasts } from './ui'
</script>
