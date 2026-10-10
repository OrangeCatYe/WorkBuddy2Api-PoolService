import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Search, Users } from 'lucide-react'
import { useAccounts, useMeta } from '@/lib/queries'
import { fmtNum, fmtRelative, pct } from '@/lib/format'
import { Card, Empty, Meter, Skeleton, StatusBadge, cx } from '@/components/ui'
import type { Account } from '@/types'

function earliestPackage(a: Account) {
  const now = Date.now()
  return (a.quota?.packages ?? [])
    .filter((p) => p.expire_at && p.remaining > 0 && new Date(p.expire_at).getTime() > now)
    .sort((x, y) => new Date(x.expire_at!).getTime() - new Date(y.expire_at!).getTime())[0]
}

export function AccountsPage() {
  const [params, setParams] = useSearchParams()
  const provider = params.get('provider') ?? ''
  const [q, setQ] = useState('')
  const { data: meta } = useMeta()
  const { data: accounts, isLoading } = useAccounts(provider || undefined)

  const rows = useMemo(() => {
    const k = q.trim().toLowerCase()
    return (accounts ?? []).filter((a) => !k || a.label.toLowerCase().includes(k) || a.id.toLowerCase().includes(k))
  }, [accounts, q])

  const tabs = [{ id: '', name: '全部' }, ...(meta?.providers ?? [])]

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex rounded-xl border border-line bg-surface p-1">
          {tabs.map((t) => (
            <button
              key={t.id}
              onClick={() => setParams(t.id ? { provider: t.id } : {})}
              className={cx('rounded-lg px-3 py-1.5 text-[13px] transition-colors', provider === t.id ? 'bg-ink text-bg' : 'text-muted hover:text-ink')}
            >
              {t.name}
            </button>
          ))}
        </div>
        <label className="flex h-9 w-64 items-center gap-2 rounded-lg border border-line bg-surface px-3 focus-within:border-accent">
          <Search size={14} className="text-faint" />
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索昵称或 ID" className="w-full bg-transparent text-[13px] outline-none" />
        </label>
      </div>

      <Card className="overflow-hidden">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b border-line text-[12px] text-faint">
              <th className="px-5 py-3 font-medium">账号</th>
              <th className="px-3 py-3 font-medium">状态</th>
              <th className="w-[220px] px-3 py-3 font-medium">积分</th>
              <th className="px-3 py-3 font-medium">最近到期</th>
              <th className="px-3 py-3 text-right font-medium">成功 / 失败</th>
              <th className="px-5 py-3 text-right font-medium">最近使用</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {isLoading &&
              [0, 1, 2].map((i) => (
                <tr key={i}>
                  <td colSpan={6} className="px-5 py-3">
                    <Skeleton className="h-6" />
                  </td>
                </tr>
              ))}
            {rows.map((a) => {
              const q = a.quota
              const pk = earliestPackage(a)
              const ratio = q ? pct(q.remaining, q.total) : 0
              return (
                <tr key={a.provider + a.id} className="text-[13.5px] hover:bg-sunken/40">
                  <td className="px-5 py-3">
                    <div className="font-medium">{a.label || a.id}</div>
                    <div className="num text-[11.5px] text-faint">
                      {a.provider} · {a.id}
                    </div>
                  </td>
                  <td className="px-3 py-3">
                    <StatusBadge status={a.status} />
                    {a.status_reason && <div className="mt-1 max-w-[180px] truncate text-[11.5px] text-faint">{a.status_reason}</div>}
                  </td>
                  <td className="px-3 py-3">
                    {q ? (
                      q.unlimited ? (
                        <span className="text-muted">不限</span>
                      ) : (
                        <>
                          <div className="num flex justify-between text-[12.5px]">
                            <span className="font-semibold">{fmtNum(q.remaining)}</span>
                            <span className="text-faint">{fmtNum(q.total)}</span>
                          </div>
                          <Meter className="mt-1.5" value={ratio} tone={ratio < 10 ? 'bad' : ratio < 30 ? 'warn' : 'accent'} />
                        </>
                      )
                    ) : (
                      <span className="text-faint">—</span>
                    )}
                  </td>
                  <td className="px-3 py-3">
                    {pk ? (
                      <>
                        <div className="num text-[13px]">{fmtNum(pk.remaining)}</div>
                        <div className="text-[11.5px] text-muted">{fmtRelative(pk.expire_at)}</div>
                      </>
                    ) : (
                      <span className="text-faint">—</span>
                    )}
                  </td>
                  <td className="num px-3 py-3 text-right">
                    <span className="text-ok">{a.stats.success}</span>
                    <span className="text-faint"> / </span>
                    <span className={a.stats.fail ? 'text-bad' : 'text-faint'}>{a.stats.fail}</span>
                  </td>
                  <td className="px-5 py-3 text-right text-[12.5px] text-muted">{fmtRelative(a.stats.last_used_at)}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
        {!isLoading && !rows.length && <Empty icon={<Users size={28} />} title="暂无账号" hint="启用平台并添加账号后会显示在这里。" />}
      </Card>
    </div>
  )
}
