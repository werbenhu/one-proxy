<template>
  <div class="suggest">
    <input
      :value="modelValue"
      :placeholder="placeholder"
      @input="onInput"
      @focus="open = true"
      @blur="open = false"
      @keydown.down.prevent="move(1)"
      @keydown.up.prevent="move(-1)"
      @keydown.enter.prevent="choose(active)"
      @keydown.esc="open = false"
    />
    <div v-if="open && filtered.length" class="suggest-list">
      <div
        v-for="(opt, i) in filtered"
        :key="opt"
        class="suggest-item"
        :class="{ active: i === active }"
        @mousedown.prevent="pick(opt)"
        @mouseenter="active = i"
      >{{ opt }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'

const props = defineProps<{ modelValue: string; options: string[]; placeholder?: string }>()
const emit = defineEmits<{ 'update:modelValue': [string] }>()

const open = ref(false)
const active = ref(-1)

const filtered = computed(() => {
  const q = props.modelValue.trim().toLowerCase()
  const list = q ? props.options.filter(o => o.toLowerCase().includes(q)) : props.options
  return list.slice(0, 50)
})

function onInput(e: Event) {
  emit('update:modelValue', (e.target as HTMLInputElement).value)
  open.value = true
  active.value = -1
}
function pick(opt: string) {
  emit('update:modelValue', opt)
  open.value = false
}
function move(step: number) {
  if (!open.value) { open.value = true; return }
  if (!filtered.value.length) return
  active.value = (active.value + step + filtered.value.length) % filtered.value.length
}
function choose(i: number) {
  if (open.value && i >= 0 && i < filtered.value.length) pick(filtered.value[i])
}
</script>
