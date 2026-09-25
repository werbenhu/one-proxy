export interface ProviderView {
  id: string
  name: string
  vendor: string
  type: string
  baseUrl: string
  apiKeyHint: string
  hasExtra: boolean
  authMode?: string
  balanceKind: string
  balanceUrl: string
  balanceKeyHint: string
  enabled: boolean
  status: string
  coolingUntil?: string
  failReason?: string
  todayTokens: number
  weekTokens: number
  monthTokens: number
}

export interface ProviderInput {
  ID: string
  Name: string
  Vendor: string
  Type: string
  BaseURL: string
  APIKey: string
  Extra: Uint8Array | null
  AuthMode: string
  BalanceKind: string
  BalanceURL: string
  BalanceKey: string
  Enabled: boolean
}

export interface ChannelTarget {
  ProviderID: string
  UpstreamModel: string
  Priority: number
  Weight: number
  Enabled: boolean
}

export interface ChannelView {
  id: string
  name: string
  model: string
  strategy: string
  targets: Array<{providerId: string; upstreamModel: string; priority: number; weight: number; enabled: boolean}>
  enabled: boolean
  healthy: number
  total: number
}

export interface ChannelInput {
  ID: string
  Name: string
  Model: string
  Strategy: string
  Targets: ChannelTarget[]
  Enabled: boolean
}

export interface PresetView {
  key: string
  displayName: string
  vendor: string
  type: string
  baseUrl: string
  balanceKind?: string
  balanceUrl?: string
  models: string[]
  mapping: Record<string, string> | null
  docsUrl?: string
}

export interface BalanceView {
  supported: boolean
  kind: string
  summary: string
  details: Array<{label: string; value: string; percent?: number; resetAt?: string}>
  checkedAt: string
}

export interface ModelInfo { id: string }

export interface ProviderKeys { apiKey: string; balanceKey: string }

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

declare global {
  interface Window {
    go: { main: { App: {
      GetProviders(): Promise<ProviderView[]>
      GetProviderKeys(id: string): Promise<ProviderKeys>
      SaveProvider(provider: ProviderInput): Promise<void>
      ExportProviders(): Promise<string>
      ImportProviders(): Promise<string>
      DeleteProvider(id: string): Promise<void>
      TestProvider(id: string): Promise<void>
      GetProviderModels(id: string): Promise<ModelInfo[]>
      GetProviderBalance(id: string): Promise<BalanceView>
      GetChannels(): Promise<ChannelView[]>
      SaveChannel(channel: ChannelInput): Promise<void>
      DeleteChannel(id: string): Promise<void>
      GetPresets(): Promise<PresetView[]>
      GetSettings(): Promise<SettingsView>
      SaveSettings(v: SettingsView): Promise<void>
      GetUsageSummary(rangeKey: string): Promise<AggRow[]>
      StartGrokDeviceAuth(id: string): Promise<DeviceAuthInfo>
      CompleteGrokDeviceAuth(id: string, deviceCode: string): Promise<void>
    } } }
  }
}

export function app() { return window.go.main.App }
