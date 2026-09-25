<template>
  <div class="shell">
    <nav class="nav">
      <div class="brand">OneProxy</div>
      <router-link to="/channels">渠道</router-link>
      <router-link to="/usage">用量</router-link>
      <router-link to="/settings">设置</router-link>
    </nav>
    <main class="main">
      <router-view />
    </main>
  </div>
</template>

<script setup lang="ts">
</script>
