import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Loader2, Plus, Search, Users, X } from 'lucide-react'
import { useAccounts, useMeta, useProviderDetail } from '@/lib/queries'
import { fmtNum, fmtRelative, pct } from '@/lib/format'
import { Badge, Button, Card, Empty, Meter, Skeleton, StatusBadge, cx } from '@/components/ui'
import { AddAccountDialog } from '@/components/AddAccountDialog'
import { AccountDrawer, ConfirmAction } from '@/components/AccountDrawer'
import { useAccountActions } from '@/components/useAccountActions'
import type { Account, AccountStatus } from '@/types'

function earliestPackage(a: Account) {
  const now = Date.now()
  return (a.quota?.packages ?? [])
    .filter((p) => p.expire_at && p.remaining > 0 && new Date(p.expire_at).getTime() > now)
    .sort((x, y) => new Date(x.expire_at!).getTime() - new Date(y.expire_at!).getTime())[0]
}

const statusFilters: { id: AccountStatus | ''; label: string }[] = [
  { id: '', label: '全部状态' },
  { id: 'active', label: '可用' },
  { id: 'cooling', label: '冷却' },
  { id: 'paused', label: '暂停' },
  { id: 'disabled', label: '禁用' },
]

const keyOf = (a: Account) => `${a.provider}/${a.id}`

function BulkBar({ provider, ids, onDone }: { provider: string; ids: string[]; onDone: () => void }) {
  const { data: detail } = useProviderDetail(provider)
  const acts = useAccountActions(provider)
  const bulk = detail?.actions?.filter((a) => a.bulk) ?? []
  return (
    <>
      <div className="flex animate-rise items-center gap-3 rounded-xl border border-ink/80 bg-ink px-4 py-2 text-bg shadow-pop">
        <span className="text-[13px]">
          已选 <b className="num">{ids.length}</b> 个账号
        </span>
        <span className="h-4 w-px bg-bg/20" />
        <div className="flex flex-wrap gap-1">
          {bulk.map((a) => (
            <button
              key={a.id}
              disabled={acts.busy}
              onClick={() => acts.run(a, ids)}
              className={cx('inline-flex items-center gap-1 rounded-md px-2.5 py-1 text-[12.5px] hover:bg-bg/10 disabled:opacity-50', a.danger && 'text-[rgb(255,150,140)]')}
            >
              {acts.busyAction === a.id && <Loader2 size={12} className="animate-spin" />}
              {a.name}
            </button>
          ))}
        </div>
        <button onClick={onDone} className="ml-auto rounded-md p-1 hover:bg-bg/10" aria-label="取消选择">
          <X size={15} />
        </button>
      </div>
      <ConfirmAction
        pending={acts.pending}
        onConfirm={() => {
          acts.confirm()
          onDone()
        }}
        onCancel={acts.cancel}
      />
    </>
  )
}

export function AccountsPage() {
  const [params, setParams] = useSearchParams()
  const provider = params.get('provider') ?? ''
  const [q, setQ] = useState('')
  const [status, setStatus] = useState<AccountStatus | ''>('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [openKey, setOpenKey] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const { data: meta } = useMeta()
  const { data: accounts, isLoading } = useAccounts(provider || undefined)

  const rows = useMemo(() => {
    const k = q.trim().toLowerCase()
    return (accounts ?? []).filter(
      (a) => (!status || a.status === status) && (!k || a.label.toLowerCase().includes(k) || a.id.toLowerCase().includes(k) || a.tags?.some((t) => t.includes(k))),
    )
  }, [accounts, q, status])

  const open = accounts?.find((a) => keyOf(a) === openKey) ?? null
  const selectedRows = rows.filter((a) => selected.has(keyOf(a)))
  const selProviders = [...new Set(selectedRows.map((a) => a.provider))]
  const allChecked = rows.length > 0 && rows.every((a) => selected.has(keyOf(a)))

  const toggle = (k: string) =>
    setSelected((s) => {
      const n = new Set(s)
      n.has(k) ? n.delete(k) : n.add(k)
      return n
    })

  const tabs = [{ id: '', name: '全部' }, ...(meta?.providers ?? [])]

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <div className="flex rounded-xl border border-line bg-surface p-1">
            {tabs.map((t) => (
              <button
                key={t.id}
                onClick={() => {
                  setParams(t.id ? { provider: t.id } : {})
                  setSelected(new Set())
                }}
                className={cx('rounded-lg px-3 py-1.5 text-[13px] transition-colors', provider === t.id ? 'bg-ink text-bg' : 'text-muted hover:text-ink')}
              >
                {t.name}
              </button>
            ))}
          </div>
          <select
            value={status}
            onChange={(e) => setStatus(e.target.value as AccountStatus | '')}
            className="h-9 rounded-lg border border-line bg-surface px-2.5 text-[13px] text-muted outline-none focus:border-accent"
          >
            {statusFilters.map((s) => (
              <option key={s.id} value={s.id}>
                {s.label}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-center gap-2">
          <label className="flex h-9 w-60 items-center gap-2 rounded-lg border border-line bg-surface px-3 focus-within:border-accent">
            <Search size={14} className="text-faint" />
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索昵称、ID、标签" className="w-full bg-transparent text-[13px] outline-none" />
          </label>
          <Button variant="primary" icon={<Plus size={15} />} onClick={() => setAdding(true)}>
            添加账号
          </Button>
        </div>
      </div>

      {selectedRows.length > 0 &&
        (selProviders.length === 1 ? (
          <BulkBar provider={selProviders[0]} ids={selectedRows.map((a) => a.id)} onDone={() => setSelected(new Set())} />
        ) : (
          <div className="rounded-xl border border-line bg-sunken px-4 py-2 text-[13px] text-muted">已选账号来自多个平台，请先切换到单个平台再批量操作。</div>
        ))}

      <Card className="overflow-hidden">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b border-line text-[12px] text-faint">
              <th className="w-10 py-3 pl-5">
                <input
                  type="checkbox"
                  className="accent-[rgb(var(--accent))]"
                  checked={allChecked}
                  onChange={() => setSelected(allChecked ? new Set() : new Set(rows.map(keyOf)))}
                  aria-label="全选"
                />
              </th>
              <th className="px-3 py-3 font-medium">账号</th>
              <th className="px-3 py-3 font-medium">状态</th>
              <th className="w-[210px] px-3 py-3 font-medium">积分</th>
              <th className="px-3 py-3 font-medium">最近到期</th>
              <th className="px-3 py-3 text-right font-medium">成功 / 失败</th>
              <th className="px-5 py-3 text-right font-medium">最近成功</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {isLoading &&
              [0, 1, 2].map((i) => (
                <tr key={i}>
                  <td colSpan={7} className="px-5 py-3">
                    <Skeleton className="h-6" />
                  </td>
                </tr>
              ))}
            {rows.map((a) => {
              const qt = a.quota
              const pk = earliestPackage(a)
              const ratio = qt ? pct(qt.remaining, qt.total) : 0
              const k = keyOf(a)
              const hoursLeft = pk?.expire_at ? (new Date(pk.expire_at).getTime() - Date.now()) / 3600_000 : Infinity
              return (
                <tr key={k} onClick={() => setOpenKey(k)} className={cx('cursor-pointer text-[13.5px] hover:bg-sunken/40', selected.has(k) && 'bg-accent/[.05]')}>
                  <td className="py-3 pl-5" onClick={(e) => e.stopPropagation()}>
                    <input type="checkbox" className="accent-[rgb(var(--accent))]" checked={selected.has(k)} onChange={() => toggle(k)} aria-label="选择" />
                  </td>
                  <td className="px-3 py-3">
                    <div className="flex items-center gap-1.5">
                      <span className="font-medium">{a.label || a.id}</span>
                      {a.tags?.map((t) => (
                        <Badge key={t} className="!px-1 !py-0 text-[10.5px]">
                          {t}
                        </Badge>
                      ))}
                    </div>
                    <div className="num text-[11.5px] text-faint">
                      {a.provider} · {a.id.length > 14 ? `${a.id.slice(0, 14)}…` : a.id}
                    </div>
                  </td>
                  <td className="px-3 py-3">
                    <StatusBadge status={a.status} />
                    {a.status_reason && a.status !== 'active' && <div className="mt-1 max-w-[170px] truncate text-[11.5px] text-faint">{a.status_reason}</div>}
                  </td>
                  <td className="px-3 py-3">
                    {qt ? (
                      qt.unlimited ? (
                        <span className="text-muted">不限</span>
                      ) : (
                        <>
                          <div className="num flex justify-between text-[12.5px]">
                            <span className="font-semibold">{fmtNum(qt.remaining)}</span>
                            <span className="text-faint">{fmtNum(qt.total)}</span>
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
                        <div className={cx('text-[11.5px]', hoursLeft < 48 ? 'text-bad' : 'text-muted')}>{fmtRelative(pk.expire_at)}</div>
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
        {!isLoading && !rows.length && (
          <Empty
            icon={<Users size={28} />}
            title={accounts?.length ? '没有匹配的账号' : '还没有账号'}
            hint={accounts?.length ? '调整筛选条件试试。' : '通过授权登录或导入凭证，把账号加入池中。'}
            action={
              !accounts?.length && (
                <Button variant="primary" icon={<Plus size={15} />} onClick={() => setAdding(true)}>
                  添加账号
                </Button>
              )
            }
          />
        )}
      </Card>

      <AccountDrawer account={open} onClose={() => setOpenKey(null)} />
      <AddAccountDialog open={adding} onClose={() => setAdding(false)} defaultProvider={provider || undefined} />
    </div>
  )
}
