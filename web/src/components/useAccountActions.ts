import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { post } from '@/lib/api'
import type { ActionDef, ActionResult } from '@/types'
import { useToast } from './overlay'

type BulkResult = { ok: number; total: number; results: { account_id: string; ok: boolean; message?: string }[] }

/** 账号动作：危险动作先二次确认；完成后刷新账号与概览。 */
export function useAccountActions(provider: string) {
  const qc = useQueryClient()
  const toast = useToast()
  const [pending, setPending] = useState<{ action: ActionDef; ids: string[] } | null>(null)

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ['accounts'] })
    qc.invalidateQueries({ queryKey: ['overview'] })
  }

  const m = useMutation({
    mutationFn: async ({ action, ids }: { action: ActionDef; ids: string[] }) => {
      if (ids.length === 1) {
        const r = await post<ActionResult>(`/api/providers/${provider}/accounts/${encodeURIComponent(ids[0])}/actions/${action.id}`)
        return { text: r.message ?? `${action.name}完成`, failed: 0 }
      }
      const r = await post<BulkResult>(`/api/providers/${provider}/actions/${action.id}`, { account_ids: ids })
      const failed = r.results.filter((x) => !x.ok)
      return { text: `${action.name}：成功 ${r.ok} / ${r.total}`, failed: failed.length, detail: failed[0]?.message }
    },
    onSuccess: (r) => {
      if (r.failed) toast.bad(`${r.text}${'detail' in r && r.detail ? `（${r.detail}）` : ''}`)
      else toast.ok(r.text)
      refresh()
    },
    onError: (e: Error) => toast.bad(e.message),
  })

  function run(action: ActionDef, ids: string[]) {
    if (!ids.length) return
    if (action.danger) setPending({ action, ids })
    else m.mutate({ action, ids })
  }

  return {
    run,
    busy: m.isPending,
    busyAction: m.isPending ? m.variables?.action.id : undefined,
    pending,
    confirm: () => {
      if (pending) m.mutate(pending)
      setPending(null)
    },
    cancel: () => setPending(null),
  }
}
