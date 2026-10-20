import { useQueries, useQuery } from '@tanstack/react-query'
import { api } from './api'
import type { Account, AgentsResponse, ApiKey, ConnectInfo, CreditsSnapshot, Decision, GatewaySettings, Meta, ModelEntry, Overview, ProviderDetail, ProviderSettings, Quest, RequestEntry, TaskRun, UpdateSettings, UpdateStatus, UsageStats } from '@/types'

export const useMeta = () => useQuery({ queryKey: ['meta'], queryFn: () => api<Meta>('/api/meta'), staleTime: 60_000 })

export const useOverview = () =>
  useQuery({ queryKey: ['overview'], queryFn: () => api<Overview>('/api/overview'), refetchInterval: 5000 })

export const useAccounts = (provider?: string) =>
  useQuery({
    queryKey: ['accounts', provider ?? 'all'],
    queryFn: () => api<{ accounts: Account[] }>(`/api/accounts${provider ? `?provider=${encodeURIComponent(provider)}` : ''}`),
    select: (d) => d.accounts,
    refetchInterval: 5000,
  })

export const useModels = () =>
  useQuery({
    queryKey: ['models'],
    queryFn: () => api<{ models: ModelEntry[]; errors: Record<string, string> }>('/api/models'),
    staleTime: 60_000,
  })

const detailQuery = (pid: string) => ({
  queryKey: ['provider', pid],
  queryFn: () => api<ProviderDetail>(`/api/providers/${encodeURIComponent(pid)}`),
  staleTime: 60_000,
})

export const useProviderDetail = (pid?: string) => useQuery({ ...detailQuery(pid ?? ''), enabled: !!pid })

/** 全部平台的能力详情（登录方式 / 动作 / 任务），按 meta.providers 顺序。 */
export function useProviderDetails() {
  const { data: meta } = useMeta()
  const ids = meta?.providers.map((p) => p.id) ?? []
  return useQueries({
    queries: ids.map(detailQuery),
    combine: (rs) => rs.map((r) => r.data).filter((d): d is ProviderDetail => !!d),
  })
}

export const useGatewaySettings = () =>
  useQuery({ queryKey: ['settings', 'gateway'], queryFn: () => api<GatewaySettings>('/api/settings/gateway') })

export const useKeys = () =>
  useQuery({ queryKey: ['keys'], queryFn: () => api<{ keys: ApiKey[] }>('/api/keys'), select: (d) => d.keys, refetchInterval: 10_000 })

export const useConnect = () => useQuery({ queryKey: ['connect'], queryFn: () => api<ConnectInfo>('/api/connect'), staleTime: 300_000 })

export const useRequests = (params: Record<string, string>, live: boolean) =>
  useQuery({
    queryKey: ['requests', params],
    queryFn: () => api<{ requests: RequestEntry[] }>(`/api/requests?${new URLSearchParams(params)}`),
    select: (d) => d.requests,
    refetchInterval: live ? 3000 : false,
  })

export const useUsageStats = (hours: number) =>
  useQuery({ queryKey: ['usage', hours], queryFn: () => api<UsageStats>(`/api/usage/stats?hours=${hours}`), refetchInterval: 15_000 })

export const useCredits = () => useQuery({ queryKey: ['credits'], queryFn: () => api<CreditsSnapshot>('/api/credits'), refetchInterval: 30_000 })

export const useDecisions = (live: boolean) =>
  useQuery({
    queryKey: ['decisions'],
    queryFn: () => api<{ decisions: Decision[] }>('/api/credits/decisions?limit=60'),
    select: (d) => d.decisions,
    refetchInterval: live ? 5000 : false,
  })

export const useProviderSettings = (pid: string) =>
  useQuery({ queryKey: ['provider-settings', pid], queryFn: () => api<ProviderSettings>(`/api/providers/${encodeURIComponent(pid)}/settings`), enabled: !!pid })

export const useTaskRuns = (params: Record<string, string>) =>
  useQuery({
    queryKey: ['task-runs', params],
    queryFn: () => api<{ runs: TaskRun[] }>(`/api/task-runs?${new URLSearchParams(params)}`),
    select: (d) => d.runs,
    refetchInterval: 5000,
  })

export const useLatestRuns = () =>
  useQuery({ queryKey: ['task-runs', 'latest'], queryFn: () => api<{ latest: Record<string, TaskRun> }>('/api/task-runs/latest'), select: (d) => d.latest, refetchInterval: 5000 })

/** 单次执行详情；运行中每 2 秒刷新，结束后停止。 */
export const useTaskRun = (id?: string | null) =>
  useQuery({
    queryKey: ['task-run', id],
    queryFn: () => api<TaskRun>(`/api/task-runs/${encodeURIComponent(id!)}`),
    enabled: !!id,
    refetchInterval: (q) => (q.state.data && q.state.data.ended_at ? false : 2000),
  })

export const useQuests = (pid?: string, aid?: string, enabled = true) =>
  useQuery({
    queryKey: ['quests', pid, aid],
    queryFn: () => api<{ quests: Quest[] }>(`/api/providers/${encodeURIComponent(pid!)}/accounts/${encodeURIComponent(aid!)}/quests`),
    select: (d) => d.quests,
    enabled: !!pid && !!aid && enabled,
    staleTime: 30_000,
    retry: false,
  })

/** 更新状态；下载中每 0.7 秒刷新进度，其余时候不轮询。 */
export const useUpdateStatus = () =>
  useQuery({
    queryKey: ['update', 'status'],
    queryFn: () => api<UpdateStatus>('/api/update/status'),
    refetchInterval: (q) => (q.state.data?.state === 'downloading' ? 700 : false),
  })

export const useUpdateSettings = () => useQuery({ queryKey: ['update', 'settings'], queryFn: () => api<UpdateSettings>('/api/settings/update') })

/** 快速导入：各 Agent 的检测结果与可导入的模型；传入密钥 ID 时模型按该密钥的白名单过滤。 */
export const useAgents = (keyId: string) =>
  useQuery({
    queryKey: ['agents', keyId],
    queryFn: () => api<AgentsResponse>(`/api/agents${keyId ? `?key_id=${encodeURIComponent(keyId)}` : ''}`),
  })
