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
  gateway: { model_naming: string }
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
