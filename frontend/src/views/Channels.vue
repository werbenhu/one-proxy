<template>
  <div>
    <div class="page-head">
      <div><h2>{{ t('nav.channels') }}</h2></div>
      <div class="actions"><button class="primary" :disabled="providers.length === 0" @click="openAdd">{{ t('channels.add') }}</button><button @click="refresh">{{ t('common.refresh') }}</button></div>
    </div>

    <table v-if="channels.length" class="channel-table">
      <thead><tr><th>{{ t('channels.colStatus') }}</th><th>{{ t('channels.name') }}</th><th>{{ t('channels.colModel') }}</th><th>{{ t('channels.strategy') }}</th><th>{{ t('channels.targets') }}</th><th>{{ t('channels.colActions') }}</th></tr></thead>
      <tbody>
        <tr v-for="channel in channels" :key="channel.id">
          <td :data-label="t('channels.colStatus')"><span v-if="!channel.enabled" class="badge disabled">{{ t('channels.statusDisabled') }}</span><span v-else-if="channel.healthy === channel.total" class="badge ok">{{ t('channels.statusOk') }}</span><span v-else-if="channel.healthy > 0" class="badge cooling">{{ t('channels.statusPartial') }}</span><span v-else class="badge auth-failed">{{ t('channels.statusUnavailable') }}</span></td>
          <td :data-label="t('channels.name')"><strong>{{ channel.name }}</strong><div class="channel-id">{{ channel.id }}</div></td>
          <td :data-label="t('channels.colModel')"><code>{{ channel.model || t('channels.wildcardModel') }}</code></td>
          <td :data-label="t('channels.strategy')">{{ channel.strategy === 'round-robin' ? t('channels.strategyRoundRobin') : t('channels.strategyPriority') }}</td>
          <td :data-label="t('channels.targets')">
            <div class="target-summary">
              <span v-for="target in channel.targets" :key="target.providerId" :class="['target-pill', {off: !target.enabled}]" :title="target.upstreamModel">
                {{ providerName(target.providerId) }}
              </span>
            </div>
          </td>
          <td :data-label="t('channels.colActions')"><div class="row-actions"><button @click="edit(channel)">{{ t('common.edit') }}</button><button class="danger" @click="remove(channel.id)">{{ t('common.delete') }}</button></div></td>
        </tr>
      </tbody>
    </table>
    <div v-else class="empty-state">{{ t('channels.empty') }}</div>

    <div v-if="modalOpen" class="modal-mask">
      <div class="modal modal-wide">
        <h3>{{ editing ? t('channels.editTitle') : t('channels.addTitle') }}</h3>
        <div class="row">
          <div class="field"><label>{{ t('channels.name') }}</label><input v-model="form.Name" :placeholder="t('channels.namePlaceholder')" /></div>
          <div class="field"><label>{{ t('channels.modelLabel') }}</label><input v-model="form.Model" :placeholder="t('channels.modelPlaceholder')" /></div>
        </div>
        <div class="field">
          <label>{{ t('channels.idLabel') }}</label>
          <input v-model="form.ID" :placeholder="editing ? '' : t('channels.idPlaceholder')" />
          <span class="field-hint">{{ t('channels.idHint') }}</span>
        </div>
        <div class="field strategy-field">
          <label>{{ t('channels.strategy') }}</label>
          <div class="segmented"><button :class="{active: form.Strategy === 'priority'}" @click="form.Strategy = 'priority'">{{ t('channels.strategyPriority') }}</button><button :class="{active: form.Strategy === 'round-robin'}" @click="form.Strategy = 'round-robin'">{{ t('channels.strategyRoundRobin') }}</button></div>
        </div>

        <div class="targets-head"><div><strong>{{ t('channels.targets') }}</strong><span>{{ t('channels.targetsHint') }}</span></div><button @click="addTarget">{{ t('channels.addTarget') }}</button></div>
        <div class="target-editor" v-for="(target, index) in form.Targets" :key="index">
          <div class="target-grid">
            <div class="field"><label>{{ t('channels.provider') }}</label><select v-model="target.ProviderID" @change="loadModels(target.ProviderID)"><option value="">{{ t('channels.pleaseSelect') }}</option><option v-for="p in providers" :key="p.id" :value="p.id">{{ p.name }}</option></select></div>
            <div class="field">
              <label>{{ t('channels.upstreamModel') }}</label>
              <SuggestInput v-model="target.UpstreamModel" :options="providerModels[target.ProviderID] || []" :placeholder="t('channels.upstreamModelPlaceholder')" />
            </div>
            <div v-if="form.Strategy === 'priority'" class="field compact"><label>{{ t('channels.priority') }}</label><input v-model.number="target.Priority" type="number" /></div>
            <div v-else class="field compact"><label>{{ t('channels.weight') }}</label><input v-model.number="target.Weight" type="number" min="1" /></div>
            <label class="check target-check"><input type="checkbox" v-model="target.Enabled" />{{ t('common.enable') }}</label>
            <button class="icon-danger" :title="t('channels.removeTarget')" @click="form.Targets.splice(index, 1)">×</button>
          </div>
        </div>
        <div v-if="form.Targets.length === 0" class="inline-empty">{{ t('channels.targetEmpty') }}</div>
        <label class="check"><input type="checkbox" v-model="form.Enabled" />{{ t('channels.enableChannel') }}</label>
        <p v-if="formError" class="error-text">{{ formError }}</p>
        <div class="actions modal-actions"><button class="primary" @click="save">{{ t('common.save') }}</button><button @click="modalOpen = false">{{ t('common.cancel') }}</button></div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { app, type ChannelInput, type ChannelTarget, type ChannelView, type ProviderView } from '../api'
import { confirmDialog } from '../ui'
import { t } from '../i18n'
import SuggestInput from '../components/SuggestInput.vue'

const channels = ref<ChannelView[]>([])
const providers = ref<ProviderView[]>([])
const modalOpen = ref(false)
const editing = ref(false)
const originalID = ref('')
const formError = ref('')
const form = ref<ChannelInput>(emptyForm())

function emptyForm(): ChannelInput { return { ID: '', Name: '', Model: '', Strategy: 'priority', Targets: [], Enabled: true } }
function emptyTarget(): ChannelTarget { return { ProviderID: providers.value[0]?.id || '', UpstreamModel: '', Priority: 10, Weight: 1, Enabled: true } }
async function refresh() { [channels.value, providers.value] = await Promise.all([app().GetChannels(), app().GetProviders()]) }
onMounted(refresh)
function providerName(id: string) { return providers.value.find(p => p.id === id)?.name || id }
const providerModels = ref<Record<string, string[]>>({})
async function loadModels(id: string) {
  if (!id || providerModels.value[id]) return
  try {
    const models = await app().GetProviderModels(id)
    providerModels.value = { ...providerModels.value, [id]: models.map(m => m.id) }
  } catch { /* 模型列表不可用时仍可手动输入 */ }
}
function openAdd() { editing.value = false; originalID.value = ''; form.value = emptyForm(); form.value.Targets.push(emptyTarget()); formError.value = ''; loadModels(form.value.Targets[0].ProviderID); modalOpen.value = true }
function addTarget() { form.value.Targets.push(emptyTarget()); loadModels(form.value.Targets[form.value.Targets.length - 1].ProviderID) }
function edit(channel: ChannelView) {
  editing.value = true; originalID.value = channel.id
  form.value = { ID: channel.id, Name: channel.name, Model: channel.model, Strategy: channel.strategy, Enabled: channel.enabled, Targets: channel.targets.map(t => ({ ProviderID: t.providerId, UpstreamModel: t.upstreamModel, Priority: t.priority, Weight: t.weight || 1, Enabled: t.enabled })) }
  form.value.Targets.forEach(t => loadModels(t.ProviderID))
  formError.value = ''; modalOpen.value = true
}
async function save() {
  if (!form.value.Name.trim()) { formError.value = t('channels.nameRequired'); return }
  if (!form.value.Targets.length || form.value.Targets.some(t => !t.ProviderID)) { formError.value = t('channels.targetFieldsRequired'); return }
  form.value.ID = form.value.ID.trim()
  if (form.value.ID && form.value.ID !== originalID.value && channels.value.some(c => c.id === form.value.ID)) { formError.value = t('channels.idDuplicate'); return }
  try { await app().SaveChannel(form.value, originalID.value); modalOpen.value = false; await refresh() } catch (e) { formError.value = String(e) }
}
async function remove(id: string) { if (!await confirmDialog(t('channels.deleteConfirm'))) return; await app().DeleteChannel(id); await refresh() }
</script>
