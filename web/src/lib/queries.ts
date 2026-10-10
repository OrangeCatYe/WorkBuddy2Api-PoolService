import { useQueries, useQuery } from '@tanstack/react-query'
import { api } from './api'
import type { Account, ApiKey, ConnectInfo, GatewaySettings, Meta, ModelEntry, Overview, ProviderDetail, RequestEntry, UsageStats } from '@/types'

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
