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

/** POST /api/providers/{pid}/export 的响应；accounts 即可直接保存、可被导入的文件内容。 */
export interface ExportResult {
  count: number
  missing?: string[]
  accounts: unknown[]
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

export interface CreditsStrategy {
  strategy: 'waste_first' | 'off'
  waste_boost: number
  expensive_rate: number
  daily_cap: number
  reserve: number
  burn_window_days: number
}

export interface PackageForecast {
  name?: string
  source?: string
  amount: number
  remaining: number
  expire_at?: string
  used: number
  waste: number
}

export interface CreditSim {
  packages?: PackageForecast[]
  waste: number
  first_waste_at?: string
  exhaust_at?: string
  required_per_day: number
  expiring_24h: number
  expiring_72h: number
  expiring_7d: number
  expiring_30d: number
  next_expire_at?: string
  next_expire_amount: number
  no_expiry: number
}

export interface AccountForecast extends CreditSim {
  provider: string
  account_id: string
  label: string
  status: AccountStatus
  remaining: number
  total: number
  burn_per_day: number
  observed_days: number
  today_credit: number
  today_requests: number
  series: number[]
  urgency: number
  capped?: boolean
  note?: string
}

export interface CreditsSnapshot {
  updated_at: string
  history_since: string
  window_days: number
  strategy: CreditsStrategy
  pool: {
    remaining: number
    burn_per_day: number
    today_credit: number
    expiring_24h: number
    expiring_72h: number
    expiring_7d: number
    expiring_30d: number
    waste: number
    ideal_waste: number
    required_per_day: number
    exhaust_at?: string
    next_expire_at?: string
    accounts: number
  }
  calendar: { date: string; expiring: number; waste: number; packages: number }[]
  accounts: AccountForecast[]
  tunable_providers: string[] | null
}

export interface Decision {
  time: string
  provider: string
  model: string
  rate: number
  chosen: string
  chosen_label: string
  fallback?: boolean
  explored?: boolean
  reason: string
  candidates?: { account_id: string; label: string; weight: number; bias: number; share: number; tier: number; note?: string }[]
}

export type SettingKind = 'bool' | 'int' | 'float' | 'duration' | 'hours' | 'select' | 'text' | 'secret'

export interface SettingField {
  key: string
  label: string
  hint?: string
  group: string
  kind: SettingKind
  options?: { value: string; label: string }[]
  min?: number
  max?: number
  unit?: string
  restart?: boolean
  enabled_by?: string
}

export interface ProviderSettings {
  fields: SettingField[]
  values: Record<string, unknown>
  secrets_set: string[]
  pending_restart: string[]
}

export type RunItemStatus = 'pending' | 'running' | 'ok' | 'skipped' | 'error'
export type RunStatus = 'running' | 'ok' | 'partial' | 'failed' | 'skipped'

export interface TaskRunItem {
  account_id: string
  account?: string
  code?: string
  title?: string
  status: RunItemStatus
  message?: string
  credit?: number
  energy?: number
  started_at?: string
  ended_at?: string
}

export interface TaskRunSummary {
  total: number
  ok: number
  skipped: number
  failed: number
  pending: number
  credit: number
  energy: number
}

export interface TaskRun {
  id: string
  provider: string
  task: string
  task_name: string
  trigger: 'schedule' | 'manual'
  scope: number
  status: RunStatus
  note?: string
  started_at: string
  ended_at?: string
  summary: TaskRunSummary
  items?: TaskRunItem[]
}

export type QuestStatus = 'locked' | 'todo' | 'claimable' | 'claimed'

export interface Quest {
  code: string
  title: string
  description?: string
  credit?: number
  energy?: number
  current: number
  target: number
  status: QuestStatus
  accepted: boolean
  tag?: string
  auto: boolean
  auto_desc?: string
  attempt?: boolean
}

// ---- 软件更新 ----

export type UpdateState = 'idle' | 'checking' | 'up_to_date' | 'available' | 'downloading' | 'ready' | 'applying' | 'error'

export interface UpdateResult {
  ok: boolean
  rolled_back?: boolean
  from: string
  to: string
  message: string
  at: string
}

export interface UpdateStatus {
  state: UpdateState
  current: { version: string; release: boolean; can_apply: boolean; reason?: string; mode: 'self' | 'supervised'; platform: string }
  latest?: { version: string; tag: string; notes?: string; published_at?: string; size?: number; signed: boolean; installable: boolean; reason?: string }
  source: string
  checked_at?: string
  error?: string
  progress?: { done: number; total: number }
  last_result?: UpdateResult
}

export interface UpdateSource {
  id: string
  name: string
  manifest_url: string
  asset_url: string
  builtin?: boolean
}

export interface UpdateSettings {
  source: string
  sources: UpdateSource[]
  proxy: string
  auto_check: boolean
  check_interval: string
}