export type AccountStatus = 'active' | 'cooling' | 'paused' | 'disabled' | 'error'

export interface CreditPackage {
  id?: string
  name?: string
  source?: string
  amount: number
  remaining: number
  expire_at?: string
}

export interface Quota {
  unit: string
  remaining: number
  total: number
  unlimited?: boolean
  packages?: CreditPackage[]
  cycle_reset_at?: string
  updated_at: string
}

export interface Account {
  id: string
  provider: string
  label: string
  status: AccountStatus
  status_reason?: string
  cooling_until?: string
  quota?: Quota
  stats: { success: number; fail: number; in_flight: number; last_used_at?: string }
  tags?: string[]
  meta?: Record<string, unknown>
}

export interface FieldDef {
  key: string
  label: string
  kind?: string
}

export interface ProviderMeta {
  id: string
  name: string
  description?: string
  icon?: string
  homepage?: string
  capabilities: string[]
  account_fields?: FieldDef[]
}

export interface Meta {
  version: string
  started_at: string
  providers: ProviderMeta[]
  gateway: { model_naming: string; credit_in_model_name: boolean }
}

export interface LoginMethod {
  id: string
  name: string
  description?: string
  fields?: FieldDef[]
}

export interface LoginSession {
  id: string
  auth_url?: string
  user_code?: string
  expires_at: string
}

export interface LoginStatus {
  state: 'pending' | 'success' | 'failed' | 'expired'
  message?: string
  account_id?: string
}

export interface ActionDef {
  id: string
  name: string
  danger?: boolean
  bulk?: boolean
  confirm?: string
}

export interface ActionResult {
  ok: boolean
  message?: string
}

export interface TaskDef {
  id: string
  name: string
  description?: string
  hours?: number[]
  enabled: boolean
}

export interface ProviderDetail {
  meta: ProviderMeta
  login_methods?: LoginMethod[]
  actions?: ActionDef[]
  tasks?: TaskDef[]
}

export interface GatewaySettings {
  model_naming: 'short' | 'full' | 'both'
  credit_in_model_name: boolean
}

export interface ProviderSummary {
  id: string
  name: string
  total: number
  active: number
  cooling: number
  paused: number
  disabled: number
  remaining: number
  quota: number
  expiring_7d: number
}

export interface Overview {
  summary: ProviderSummary
  providers: ProviderSummary[]
  now: string
}

export interface ModelEntry {
  id: string
  provider: string
  display_name: string
  description?: string
  vendor?: string
  context_window?: number
  max_output_tokens?: number
  input_modalities: string[]
  supports_tools: boolean
  supports_reasoning?: boolean
  reasoning_levels?: string[]
  reasoning_default_effort?: string
  credit_rate?: number
  credits?: string
  tags?: string[]
}

export interface KeyUsage {
  requests: number
  failures: number
  prompt_tokens: number
  completion_tokens: number
  cached_tokens: number
  credit: number
  day?: string
  day_credit: number
  day_requests: number
  last_used_at?: string
}

export interface ApiKey {
  id: string
  name: string
  secret: string
  enabled: boolean
  created_at: string
  expires_at?: string
  note?: string
  models?: string[]
  rpm?: number
  credit_budget?: number
  daily_credit_budget?: number
  usage: KeyUsage
  builtin?: boolean
}

export interface KeySpec {
  name: string
  enabled?: boolean
  expires_at?: string | null
  note: string
  models: string[]
  rpm: number
  credit_budget: number
  daily_credit_budget: number
}

export type Protocol = 'chat' | 'messages' | 'responses'

export interface RequestEntry {
  id: string
  time: string
  protocol: Protocol
  key_id?: string
  key_name?: string
  provider?: string
  account_id?: string
  model?: string
  requested_model?: string
  stream: boolean
  effort?: string
  effort_requested?: string
  status: number
  ok: boolean
  error_kind?: string
  error?: string
  attempts?: number
  prompt_tokens: number
  completion_tokens: number
  cached_tokens: number
  reasoning_tokens?: number
  credit: number
  has_credit?: boolean
  ttfb_ms: number
  duration_ms: number
  client_ip?: string
  user_agent?: string
}

export interface UsageBucket {
  key: string
  requests: number
  failures: number
  prompt_tokens: number
  completion_tokens: number
  cached_tokens: number
  credit: number
  avg_ttfb_ms: number
}

export interface UsageStats {
  since: string
  total: UsageBucket
  series: UsageBucket[]
  by_model: UsageBucket[]
  by_key: UsageBucket[]
  by_account: UsageBucket[]
  by_protocol: UsageBucket[]
}

export interface ConnectInfo {
  base_url: string
  openai: string
  anthropic: string
  listen: string
  has_legacy: boolean
}
