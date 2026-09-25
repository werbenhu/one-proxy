// Wails bindings 类型声明（go 方法签名对齐）。

export interface ChannelView {
  id: string
  name: string
  type: string
  baseUrl: string
  apiKeyHint: string
  hasExtra: boolean
  models: string[]
  modelMapping: Record<string, string> | null
  priority: number
  enabled: boolean
  status: string
  coolingUntil?: string
  failReason?: string
  todayTokens: number
}

export interface ChannelInput {
  ID: string
  Name: string
  Type: string
  BaseURL: string
  APIKey: string
  Extra: Uint8Array | null
  Models: string[]
  ModelMapping: Record<string, string> | null
  Priority: number
  Enabled: boolean
}

export interface PresetView {
  key: string
  displayName: string
  type: string
  baseUrl: string
  models: string[]
  mapping: Record<string, string> | null
  docsUrl?: string
}

export interface SettingsView {
  listenHost: string
  listenPort: number
  localKey: string
  retainDays: number
}

export interface AggRow {
  channelId: string
  channelName: string
  modelRequested: string
  modelUpstream: string
  requests: number
  inputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheWriteTokens: number
  errors: number
}

export interface DeviceAuthInfo {
  deviceCode: string
  userCode: string
  verificationUri: string
  verificationUriComplete?: string
  expiresInSeconds: number
  intervalSeconds: number
}

// Wails 运行时注入的绑定对象
declare global {
  interface Window {
    go: {
      main: {
        App: {
          GetChannels(): Promise<ChannelView[]>
          SaveChannel(ch: ChannelInput): Promise<void>
          DeleteChannel(id: string): Promise<void>
          GetPresets(): Promise<PresetView[]>
          GetSettings(): Promise<SettingsView>
          SaveSettings(v: SettingsView): Promise<void>
          GetUsageSummary(rangeKey: string): Promise<AggRow[]>
          TestChannel(id: string): Promise<void>
          StartGrokDeviceAuth(id: string): Promise<DeviceAuthInfo>
          CompleteGrokDeviceAuth(id: string, deviceCode: string): Promise<void>
        }
      }
    }
  }
}

export function app() {
  return window.go.main.App
}
