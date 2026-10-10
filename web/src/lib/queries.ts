import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import type { Account, Meta, ModelEntry, Overview } from '@/types'

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
