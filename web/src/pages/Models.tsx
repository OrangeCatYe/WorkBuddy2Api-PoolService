import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Boxes, Brain, Image, RefreshCw, Search, Wrench } from 'lucide-react'
import { post } from '@/lib/api'
import { useModels } from '@/lib/queries'
import { fmtTokens } from '@/lib/format'
import { Badge, Button, Card, Empty, Skeleton, cx } from '@/components/ui'

type Filter = 'tools' | 'vision' | 'reasoning'

export function ModelsPage() {
  const { data, isLoading } = useModels()
  const qc = useQueryClient()
  const [q, setQ] = useState('')
  const [filters, setFilters] = useState<Set<Filter>>(new Set())
  const [refreshing, setRefreshing] = useState(false)

  const rows = useMemo(() => {
    const k = q.trim().toLowerCase()
    return (data?.models ?? []).filter((m) => {
      if (k && !`${m.id} ${m.display_name} ${m.vendor ?? ''} ${(m.tags ?? []).join(' ')}`.toLowerCase().includes(k)) return false
      if (filters.has('tools') && !m.supports_tools) return false
      if (filters.has('vision') && !m.input_modalities.includes('image')) return false
      if (filters.has('reasoning') && !m.reasoning_levels?.length) return false
      return true
    })
  }, [data, q, filters])

  const toggle = (f: Filter) =>
    setFilters((s) => {
      const n = new Set(s)
      n.has(f) ? n.delete(f) : n.add(f)
      return n
    })

  async function refresh() {
    setRefreshing(true)
    try {
      const d = await post('/api/models/refresh')
      qc.setQueryData(['models'], d)
    } finally {
      setRefreshing(false)
    }
  }

  const chips: { id: Filter; label: string; icon: JSX.Element }[] = [
    { id: 'tools', label: '工具调用', icon: <Wrench size={13} /> },
    { id: 'vision', label: '视觉', icon: <Image size={13} /> },
    { id: 'reasoning', label: '思考', icon: <Brain size={13} /> },
  ]

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <label className="flex h-9 w-72 items-center gap-2 rounded-lg border border-line bg-surface px-3 focus-within:border-accent">
            <Search size={14} className="text-faint" />
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="模型 ID、名称、厂商、标签" className="w-full bg-transparent text-[13px] outline-none" />
          </label>
          {chips.map((c) => (
            <button
              key={c.id}
              onClick={() => toggle(c.id)}
              className={cx(
                'inline-flex h-9 items-center gap-1.5 rounded-lg border px-3 text-[13px] transition-colors',
                filters.has(c.id) ? 'border-accent bg-accent/10 text-accent' : 'border-line bg-surface text-muted hover:text-ink',
              )}
            >
              {c.icon}
              {c.label}
            </button>
          ))}
        </div>
        <Button onClick={refresh} disabled={refreshing} icon={<RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />}>
          刷新目录
        </Button>
      </div>

      {data?.errors && Object.keys(data.errors).length > 0 && (
        <Card className="border-warn/40 bg-warn/5 px-5 py-3 text-[13px] text-warn">
          {Object.entries(data.errors).map(([p, e]) => (
            <div key={p}>
              {p}：{e}（已沿用上次目录）
            </div>
          ))}
        </Card>
      )}

      <Card className="overflow-hidden">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b border-line text-[12px] text-faint">
              <th className="px-5 py-3 font-medium">模型</th>
              <th className="px-3 py-3 text-right font-medium">上下文</th>
              <th className="px-3 py-3 text-right font-medium">最大输出</th>
              <th className="px-3 py-3 font-medium">能力</th>
              <th className="px-3 py-3 font-medium">思考档位</th>
              <th className="px-5 py-3 text-right font-medium">倍率</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {isLoading &&
              [0, 1, 2, 3].map((i) => (
                <tr key={i}>
                  <td colSpan={6} className="px-5 py-3">
                    <Skeleton className="h-6" />
                  </td>
                </tr>
              ))}
            {rows.map((m) => (
              <tr key={m.id} className="text-[13.5px] hover:bg-sunken/40">
                <td className="px-5 py-3">
                  <div className="font-medium">{m.display_name}</div>
                  <div className="num text-[11.5px] text-faint">{m.id}</div>
                </td>
                <td className="num px-3 py-3 text-right">{fmtTokens(m.context_window)}</td>
                <td className="num px-3 py-3 text-right">{fmtTokens(m.max_output_tokens)}</td>
                <td className="px-3 py-3">
                  <div className="flex gap-1">
                    {m.supports_tools && <Badge tone="info">工具</Badge>}
                    {m.input_modalities.includes('image') && <Badge tone="ok">视觉</Badge>}
                    {m.supports_reasoning && <Badge tone="accent">思考</Badge>}
                  </div>
                </td>
                <td className="px-3 py-3">
                  <div className="flex flex-wrap gap-1">
                    {m.reasoning_levels?.map((l) => (
                      <span key={l} className={cx('num rounded px-1.5 py-0.5 text-[11px]', l === m.reasoning_default_effort ? 'bg-ink text-bg' : 'bg-sunken text-muted')}>
                        {l}
                      </span>
                    )) ?? <span className="text-faint">—</span>}
                  </div>
                </td>
                <td className="num px-5 py-3 text-right">
                  {m.credit_rate === 0 ? <Badge tone="ok">免费</Badge> : (m.credits ?? (m.credit_rate !== undefined ? `x${m.credit_rate}` : '—'))}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {!isLoading && !rows.length && <Empty icon={<Boxes size={28} />} title="没有匹配的模型" hint="调整筛选条件，或刷新目录。" />}
      </Card>
    </div>
  )
}
