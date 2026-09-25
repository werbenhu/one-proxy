import { ref } from 'vue'

export interface ToastItem { id: number; kind: 'ok' | 'error'; text: string }

export const toasts = ref<ToastItem[]>([])
let seq = 0

export function toast(text: string, kind: 'ok' | 'error' = 'ok', ms = 3000) {
  const id = ++seq
  toasts.value.push({ id, kind, text })
  setTimeout(() => {
    toasts.value = toasts.value.filter(item => item.id !== id)
  }, ms)
}

export const confirmBox = ref<{ text: string; resolve: (ok: boolean) => void } | null>(null)

export function confirmDialog(text: string): Promise<boolean> {
  return new Promise(resolve => { confirmBox.value = { text, resolve } })
}

export function settleConfirm(ok: boolean) {
  confirmBox.value?.resolve(ok)
  confirmBox.value = null
}
