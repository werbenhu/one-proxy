<template>
  <div>
    <div class="page-head">
      <div><h2>渠道</h2></div>
      <div class="actions"><button class="primary" :disabled="providers.length === 0" @click="openAdd">添加渠道</button><button @click="refresh">刷新</button></div>
    </div>

    <table v-if="channels.length" class="channel-table">
      <thead><tr><th>状态</th><th>渠道名称</th><th>对外模型</th><th>调度策略</th><th>内部目标</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="channel in channels" :key="channel.id">
          <td data-label="状态"><span v-if="!channel.enabled" class="badge disabled">已禁用</span><span v-else-if="channel.healthy === channel.total" class="badge ok">正常</span><span v-else-if="channel.healthy > 0" class="badge cooling">部分可用</span><span v-else class="badge auth-failed">不可用</span></td>
          <td data-label="渠道名称"><strong>{{ channel.name }}</strong></td>
          <td data-label="对外模型"><code>{{ channel.model }}</code></td>
          <td data-label="调度策略">{{ channel.strategy === 'round-robin' ? '加权轮询' : '优先级主备' }}</td>
          <td data-label="内部目标">
            <div class="target-summary">
              <span v-for="target in channel.targets" :key="target.providerId" :class="['target-pill', {off: !target.enabled}]" :title="target.upstreamModel">
                {{ providerName(target.providerId) }} · {{ channel.strategy === 'round-robin' ? `权重 ${target.weight || 1}` : `P${target.priority}` }}
              </span>
            </div>
          </td>
          <td data-label="操作"><div class="row-actions"><button @click="edit(channel)">编辑</button><button class="danger" @click="remove(channel.id)">删除</button></div></td>
        </tr>
      </tbody>
    </table>
    <div v-else class="empty-state">还没有对外渠道。每个渠道暴露一个模型，并把请求调度到一个或多个提供商。</div>

    <div v-if="modalOpen" class="modal-mask" @click.self="modalOpen = false">
      <div class="modal modal-wide">
        <h3>{{ form.ID ? '编辑渠道' : '添加渠道' }}</h3>
        <div class="row">
          <div class="field"><label>渠道名称</label><input v-model="form.Name" placeholder="如 Claude Sonnet" /></div>
          <div class="field"><label>对外模型名</label><input v-model="form.Model" placeholder="claude-sonnet-4-6" /></div>
        </div>
        <div class="field strategy-field">
          <label>调度策略</label>
          <div class="segmented"><button :class="{active: form.Strategy === 'priority'}" @click="form.Strategy = 'priority'">优先级主备</button><button :class="{active: form.Strategy === 'round-robin'}" @click="form.Strategy = 'round-robin'">加权轮询</button></div>
        </div>

        <div class="targets-head"><div><strong>内部目标</strong><span>请求会按下列规则发送到上游账户</span></div><button @click="addTarget">添加目标</button></div>
        <div class="target-editor" v-for="(target, index) in form.Targets" :key="index">
          <div class="target-grid">
            <div class="field"><label>提供商</label><select v-model="target.ProviderID" @change="loadModels(target.ProviderID)"><option value="">请选择</option><option v-for="p in providers" :key="p.id" :value="p.id">{{ p.name }}</option></select></div>
            <div class="field">
              <label>上游模型</label>
              <input v-model="target.UpstreamModel" :list="'upstream-models-' + index" placeholder="上游实际模型名" />
              <datalist :id="'upstream-models-' + index"><option v-for="m in providerModels[target.ProviderID] || []" :key="m" :value="m" /></datalist>
            </div>
            <div v-if="form.Strategy === 'priority'" class="field compact"><label>优先级</label><input v-model.number="target.Priority" type="number" /></div>
            <div v-else class="field compact"><label>权重</label><input v-model.number="target.Weight" type="number" min="1" /></div>
            <label class="check target-check"><input type="checkbox" v-model="target.Enabled" />启用</label>
            <button class="icon-danger" title="移除目标" @click="form.Targets.splice(index, 1)">×</button>
          </div>
        </div>
        <div v-if="form.Targets.length === 0" class="inline-empty">至少添加一个内部目标</div>
        <label class="check"><input type="checkbox" v-model="form.Enabled" />启用此渠道</label>
        <p v-if="formError" class="error-text">{{ formError }}</p>
        <div class="actions modal-actions"><button class="primary" @click="save">保存</button><button @click="modalOpen = false">取消</button></div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { app, type ChannelInput, type ChannelTarget, type ChannelView, type ProviderView } from '../api'
import { confirmDialog } from '../ui'

const channels = ref<ChannelView[]>([])
const providers = ref<ProviderView[]>([])
const modalOpen = ref(false)
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
function openAdd() { form.value = emptyForm(); form.value.Targets.push(emptyTarget()); formError.value = ''; loadModels(form.value.Targets[0].ProviderID); modalOpen.value = true }
function addTarget() { form.value.Targets.push(emptyTarget()); loadModels(form.value.Targets[form.value.Targets.length - 1].ProviderID) }
function edit(channel: ChannelView) {
  form.value = { ID: channel.id, Name: channel.name, Model: channel.model, Strategy: channel.strategy, Enabled: channel.enabled, Targets: channel.targets.map(t => ({ ProviderID: t.providerId, UpstreamModel: t.upstreamModel, Priority: t.priority, Weight: t.weight || 1, Enabled: t.enabled })) }
  form.value.Targets.forEach(t => loadModels(t.ProviderID))
  formError.value = ''; modalOpen.value = true
}
async function save() {
  if (!form.value.Name.trim() || !form.value.Model.trim()) { formError.value = '请填写渠道名称和对外模型名'; return }
  if (!form.value.Targets.length || form.value.Targets.some(t => !t.ProviderID || !t.UpstreamModel.trim())) { formError.value = '每个内部目标都需要提供商和上游模型'; return }
  try { await app().SaveChannel(form.value); modalOpen.value = false; await refresh() } catch (e) { formError.value = String(e) }
}
async function remove(id: string) { if (!await confirmDialog('确认删除这个对外渠道？')) return; await app().DeleteChannel(id); await refresh() }
</script>
