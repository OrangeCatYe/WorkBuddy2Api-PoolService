import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { ArrowDownUp, CalendarClock, ChevronUp, Download, Loader2, Plus, Search, Users, X } from 'lucide-react'
import { post } from '@/lib/api'
import { useAccounts, useMeta, useProviderDetail } from '@/lib/queries'
import { fmtNum, fmtRelative, pct } from '@/lib/format'
import { useToast } from '@/components/overlay'
import { Badge, Button, Card, Empty, Meter, Skeleton, StatusBadge, cx } from '@/components/ui'
import { AddAccountDialog } from '@/components/AddAccountDialog'
import { ExportAccountsDialog } from '@/components/ExportAccountsDialog'
import { AccountDrawer, ConfirmAction } from '@/components/AccountDrawer'
import { useAccountActions } from '@/components/useAccountActions'
import type { Account, AccountStatus } from '@/types'

const now = () => Date.now()

function earliestPackage(a: Account) {
  const t = now()
  return (a.quota?.packages ?? [])
    .filter((p) => p.expire_at && p.remaining > 0 && new Date(p.expire_at).getTime() > t)
    .sort((x, y) => new Date(x.expire_at!).getTime() - new Date(y.expire_at!).getTime())[0]
}

const statusOrder: AccountStatus[] = ['active', 'cooling', 'paused', 'disabled', 'error']
const statusLabel: Record<AccountStatus | '', string> = { '': '全部', active: '可用', cooling: '冷却', paused: '暂停', disabled: '禁用', error: '异常' }

type SortKey = 'status' | 'credits' | 'expiry' | 'recent' | 'fail'
const sorts: { id: SortKey; label: string }[] = [
  { id: 'status', label: '按状态' },
  { id: 'credits', label: '余额从高到低' },
  { id: 'expiry', label: '最近到期优先' },
  { id: 'recent', label: '最近使用' },
  { id: 'fail', label: '失败最多' },
]

const time = (iso?: string) => (iso ? new Date(iso).getTime() : 0)
const comparators: Record<SortKey, (a: Account, b: Account) => number> = {
  status: (a, b) => statusOrder.indexOf(a.status) - statusOrder.indexOf(b.status) || (b.quota?.remaining ?? 0) - (a.quota?.remaining ?? 0),
  credits: (a, b) => (b.quota?.remaining ?? -1) - (a.quota?.remaining ?? -1),
  expiry: (a, b) => (time(earliestPackage(a)?.expire_at) || Infinity) - (time(earliestPackage(b)?.expire_at) || Infinity),
  recent: (a, b) => time(b.stats.last_used_at) - time(a.stats.last_used_at),
  fail: (a, b) => b.stats.fail - a.stats.fail,
}

const keyOf = (a: Account) => `${a.provider}/${a.id}`

// ---------------- 批量操作栏 ----------------

function TaskMenu({ provider, ids, onDone }: { provider: string; ids: string[]; onDone: () => void }) {
  const { data: detail } = useProviderDetail(provider)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const toast = useToast()
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false)
    window.addEventListener('mousedown', h)
    return () => window.removeEventListener('mousedown', h)
  }, [open])
  const tasks = detail?.tasks ?? []
  if (!tasks.length) return null
  async function run(id: string, name: string) {
    setBusy(id)
    try {
      await post(`/api/providers/${provider}/tasks/${id}/run`, { account_ids: ids })
      toast.ok(`${name}：已对 ${ids.length} 个账号开始执行`)
      setOpen(false)
      onDone()
    } catch (e) {
      toast.bad((e as Error).message)
    } finally {
      setBusy(null)
    }
  }
  return (
    <div ref={ref} className="relative">
      <button onClick={() => setOpen(!open)} className="inline-flex items-center gap-1 rounded-md px-2.5 py-1 text-[12.5px] hover:bg-bg/10">
        <CalendarClock size={13} /> 执行任务 <ChevronUp size={12} className={cx('transition-transform', !open && 'rotate-180')} />
      </button>
      {open && (
        <ul className="absolute bottom-full left-0 mb-2 w-56 animate-rise overflow-hidden rounded-xl border border-line bg-surface py-1 text-ink shadow-pop">
          {tasks.map((t) => (
            <li key={t.id}>
              <button disabled={!!busy} onClick={() => run(t.id, t.name)} className="flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-[13px] hover:bg-sunken disabled:opacity-50">
                <span className="min-w-0">
                  <span className="block">{t.name}</span>
                  {t.description && <span className="block truncate text-[11.5px] text-faint">{t.description}</span>}
                </span>
                {busy === t.id && <Loader2 size={13} className="shrink-0 animate-spin" />}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function BulkActions({ provider, ids, onDone }: { provider: string; ids: string[]; onDone: () => void }) {
  const { data: detail } = useProviderDetail(provider)
  const acts = useAccountActions(provider)
  const bulk = detail?.actions?.filter((a) => a.bulk) ?? []
  if (detail && !bulk.length && !detail.tasks?.length) return <span className="px-1 text-[12.5px] text-bg/60">该平台没有可批量执行的操作</span>
  return (
    <>
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
      <TaskMenu provider={provider} ids={ids} onDone={onDone} />
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

function BulkBar({ rows, onKeep, onClear, onExport }: { rows: Account[]; onKeep: (provider: string) => void; onClear: () => void; onExport: () => void }) {
  const { data: meta } = useMeta()
  const byProvider = useMemo(() => {
    const m = new Map<string, string[]>()
    rows.forEach((a) => m.set(a.provider, [...(m.get(a.provider) ?? []), a.id]))
    return [...m.entries()]
  }, [rows])
  const nameOf = (id: string) => meta?.providers.find((p) => p.id === id)?.name ?? id
  const canExport = byProvider.length === 1 && !!meta?.providers.find((p) => p.id === byProvider[0][0])?.capabilities.includes('export')
  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-5 z-30 flex justify-center px-4 lg:pl-[232px]">
      <div className="pointer-events-auto flex max-w-full animate-rise flex-wrap items-center gap-x-3 gap-y-1.5 rounded-2xl border border-ink/80 bg-ink px-4 py-2 text-bg shadow-pop">
        <span className="text-[13px]">
          已选 <b className="num">{rows.length}</b> 个
        </span>
        <span className="h-4 w-px bg-bg/20" />
        {byProvider.length === 1 ? (
          <div className="flex flex-wrap items-center gap-0.5">
            <BulkActions provider={byProvider[0][0]} ids={byProvider[0][1]} onDone={onClear} />
            {canExport && (
              <button onClick={onExport} className="inline-flex items-center gap-1 rounded-md px-2.5 py-1 text-[12.5px] hover:bg-bg/10">
                <Download size={13} /> 导出
              </button>
            )}
          </div>
        ) : (
          <div className="flex flex-wrap items-center gap-1.5 text-[12.5px]">
            <span className="text-bg/70">来自多个平台，只保留：</span>
            {byProvider.map(([pid, ids]) => (
              <button key={pid} onClick={() => onKeep(pid)} className="rounded-md bg-bg/10 px-2 py-0.5 hover:bg-bg/20">
                {nameOf(pid)} {ids.length}
              </button>
            ))}
          </div>
        )}
        <button onClick={onClear} className="ml-auto rounded-md p-1 hover:bg-bg/10" aria-label="取消选择" title="取消选择（Esc）">
          <X size={15} />
        </button>
      </div>
    </div>
  )
}

// ---------------- 单元格 ----------------

function QuotaCell({ a }: { a: Account }) {
  const qt = a.quota
  if (!qt) return <span className="text-faint">—</span>
  if (qt.unlimited) return <span className="text-muted">不限</span>
  const ratio = pct(qt.remaining, qt.total)
  return (
    <>
      <div className="num flex justify-between text-[12.5px]">
        <span className="font-semibold">{fmtNum(qt.remaining)}</span>
        <span className="text-faint">{fmtNum(qt.total)}</span>
      </div>
      <Meter className="mt-1.5" value={ratio} tone={ratio < 10 ? 'bad' : ratio < 30 ? 'warn' : 'accent'} />
    </>
  )
}

function ExpiryCell({ a }: { a: Account }) {
  const pk = earliestPackage(a)
  if (!pk) return <span className="text-faint">—</span>
  const hours = (time(pk.expire_at) - now()) / 3600_000
  return (
    <>
      <div className="num text-[13px]">{fmtNum(pk.remaining)}</div>
      <div className={cx('text-[11.5px]', hours < 48 ? 'text-bad' : 'text-muted')}>{fmtRelative(pk.expire_at)}</div>
    </>
  )
}

function Label({ a }: { a: Account }) {
  return (
    <>
      <div className="flex min-w-0 items-center gap-1.5">
        <span className="truncate font-medium">{a.label || a.id}</span>
        {a.tags?.map((t) => (
          <Badge key={t} className="!px-1 !py-0 text-[10.5px]">
            {t}
          </Badge>
        ))}
      </div>
      <div className="num truncate text-[11.5px] text-faint">
        {a.provider} · {a.id.length > 14 ? `${a.id.slice(0, 14)}…` : a.id}
      </div>
    </>
  )
}

const checkbox = 'h-4 w-4 accent-[rgb(var(--accent))]'

// ---------------- 页面 ----------------

export function AccountsPage() {
  const [params, setParams] = useSearchParams()
  const provider = params.get('provider') ?? ''
  const status = (params.get('status') ?? '') as AccountStatus | ''
  const sort = (params.get('sort') as SortKey) || 'status'
  const [q, setQ] = useState(params.get('q') ?? '')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [openKey, setOpenKey] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const [exporting, setExporting] = useState<{ open: boolean; keys: string[] }>({ open: false, keys: [] })
  const lastIdx = useRef<number | null>(null)
  const { data: meta } = useMeta()
  const { data: accounts, isLoading } = useAccounts(provider || undefined)
  const canExport = !!meta?.providers.some((p) => p.capabilities.includes('export')) && !!accounts?.length

  const setParam = (k: string, v: string) =>
    setParams(
      (p) => {
        const n = new URLSearchParams(p)
        v ? n.set(k, v) : n.delete(k)
        return n
      },
      { replace: true },
    )

  // 搜索词防抖写入地址栏，便于分享与返回
  useEffect(() => {
    const t = setTimeout(() => setParam('q', q.trim()), 250)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q])

  const counts = useMemo(() => {
    const c: Record<string, number> = { '': accounts?.length ?? 0 }
    accounts?.forEach((a) => (c[a.status] = (c[a.status] ?? 0) + 1))
    return c
  }, [accounts])

  const rows = useMemo(() => {
    const k = q.trim().toLowerCase()
    return (accounts ?? [])
      .filter((a) => (!status || a.status === status) && (!k || a.label.toLowerCase().includes(k) || a.id.toLowerCase().includes(k) || a.tags?.some((t) => t.toLowerCase().includes(k))))
      .sort(comparators[sort])
  }, [accounts, q, status, sort])

  useEffect(() => {
    if (!selected.size) return
    const h = (e: KeyboardEvent) => e.key === 'Escape' && !openKey && setSelected(new Set())
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [selected.size, openKey])

  const open = accounts?.find((a) => keyOf(a) === openKey) ?? null
  const selectedRows = rows.filter((a) => selected.has(keyOf(a)))
  const allChecked = rows.length > 0 && rows.every((a) => selected.has(keyOf(a)))
  const someChecked = !allChecked && rows.some((a) => selected.has(keyOf(a)))

  /** 点击复选框；按住 Shift 时选中上次点击到本次之间的整段。 */
  const toggle = (i: number, shift: boolean) => {
    const k = keyOf(rows[i])
    setSelected((s) => {
      const n = new Set(s)
      const on = !n.has(k)
      if (shift && lastIdx.current !== null) {
        const [from, to] = [Math.min(lastIdx.current, i), Math.max(lastIdx.current, i)]
        rows.slice(from, to + 1).forEach((a) => (on ? n.add(keyOf(a)) : n.delete(keyOf(a))))
      } else {
        on ? n.add(k) : n.delete(k)
      }
      return n
    })
    lastIdx.current = i
  }

  const tabs = [{ id: '', name: '全部平台' }, ...(meta?.providers ?? [])]
  const statusChips: (AccountStatus | '')[] = ['', ...statusOrder.filter((s) => counts[s])]
  if (status && !statusChips.includes(status)) statusChips.push(status)

  return (
    <div className={cx('space-y-4', selectedRows.length > 0 && 'pb-20')}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          {tabs.length > 2 && (
            <div className="flex max-w-full overflow-x-auto rounded-xl border border-line bg-surface p-1">
              {tabs.map((t) => (
                <button
                  key={t.id}
                  onClick={() => {
                    setParam('provider', t.id)
                    setSelected(new Set())
                  }}
                  className={cx('shrink-0 rounded-lg px-3 py-1.5 text-[13px] transition-colors', provider === t.id ? 'bg-ink text-bg' : 'text-muted hover:text-ink')}
                >
                  {t.name}
                </button>
              ))}
            </div>
          )}
          <div className="flex flex-wrap gap-1">
            {statusChips.map((s) => (
              <button
                key={s || 'all'}
                onClick={() => setParam('status', s)}
                className={cx(
                  'inline-flex h-8 items-center gap-1.5 rounded-lg border px-2.5 text-[12.5px] transition-colors',
                  status === s ? 'border-ink bg-ink text-bg' : 'border-line bg-surface text-muted hover:text-ink',
                )}
              >
                {statusLabel[s]}
                <span className={cx('num text-[11.5px]', status === s ? 'text-bg/70' : 'text-faint')}>{counts[s] ?? 0}</span>
              </button>
            ))}
          </div>
        </div>
        <div className="flex w-full flex-wrap items-center gap-2 sm:w-auto">
          <label className="order-first flex h-9 w-full items-center gap-2 rounded-lg border border-line bg-surface px-3 focus-within:border-accent sm:order-none sm:w-56">
            <Search size={14} className="shrink-0 text-faint" />
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索昵称、ID、标签" className="w-full min-w-0 bg-transparent text-[13px] outline-none" />
            {q && (
              <button onClick={() => setQ('')} className="text-faint hover:text-ink" aria-label="清空">
                <X size={13} />
              </button>
            )}
          </label>
          <label className="flex h-9 flex-1 items-center gap-1.5 rounded-lg border border-line bg-surface pl-2.5 text-muted focus-within:border-accent sm:flex-none">
            <ArrowDownUp size={13} />
            <select value={sort} onChange={(e) => setParam('sort', e.target.value === 'status' ? '' : e.target.value)} className="h-full w-full bg-transparent pr-2 text-[13px] outline-none" aria-label="排序">
              {sorts.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.label}
                </option>
              ))}
            </select>
          </label>
          {canExport && (
            <Button icon={<Download size={15} />} onClick={() => setExporting({ open: true, keys: [] })}>
              导出配置
            </Button>
          )}
          <Button variant="primary" icon={<Plus size={15} />} onClick={() => setAdding(true)}>
            添加账号
          </Button>
        </div>
      </div>

      <Card className="overflow-hidden">
        {/* 宽屏：表格 */}
        <div className="hidden overflow-x-auto md:block">
          <table className="w-full min-w-[820px] text-left">
            <thead>
              <tr className="border-b border-line text-[12px] text-faint">
                <th className="w-10 py-3 pl-5">
                  <input
                    type="checkbox"
                    className={checkbox}
                    checked={allChecked}
                    ref={(el) => {
                      if (el) el.indeterminate = someChecked
                    }}
                    onChange={() => setSelected(allChecked ? new Set() : new Set(rows.map(keyOf)))}
                    aria-label="全选当前筛选结果"
                  />
                </th>
                <th className="px-3 py-3 font-medium">账号</th>
                <th className="px-3 py-3 font-medium">状态</th>
                <th className="w-[200px] px-3 py-3 font-medium">积分</th>
                <th className="px-3 py-3 font-medium">最近到期</th>
                <th className="px-3 py-3 text-right font-medium">成功 / 失败</th>
                <th className="px-5 py-3 text-right font-medium">最近使用</th>
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
              {rows.map((a, i) => {
                const k = keyOf(a)
                return (
                  <tr key={k} onClick={() => setOpenKey(k)} className={cx('cursor-pointer text-[13.5px] hover:bg-sunken/40', selected.has(k) && 'bg-accent/[.06]')}>
                    <td className="py-3 pl-5" onClick={(e) => e.stopPropagation()}>
                      <input
                        type="checkbox"
                        className={checkbox}
                        checked={selected.has(k)}
                        onClick={(e) => toggle(i, e.shiftKey)}
                        onChange={() => {}}
                        aria-label="选择"
                      />
                    </td>
                    <td className="max-w-[260px] px-3 py-3">
                      <Label a={a} />
                    </td>
                    <td className="px-3 py-3">
                      <StatusBadge status={a.status} />
                      {a.status_reason && a.status !== 'active' && (
                        <div className="mt-1 max-w-[170px] truncate text-[11.5px] text-faint" title={a.status_reason}>
                          {a.status_reason}
                        </div>
                      )}
                    </td>
                    <td className="px-3 py-3">
                      <QuotaCell a={a} />
                    </td>
                    <td className="px-3 py-3">
                      <ExpiryCell a={a} />
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
        </div>

        {/* 窄屏：卡片列表 */}
        <div className="md:hidden">
          {rows.length > 0 && (
            <label className="flex items-center gap-2.5 border-b border-line px-4 py-2.5 text-[12.5px] text-muted">
              <input type="checkbox" className={checkbox} checked={allChecked} onChange={() => setSelected(allChecked ? new Set() : new Set(rows.map(keyOf)))} />
              全选 {rows.length} 个
            </label>
          )}
          {isLoading && (
            <div className="space-y-2 p-4">
              <Skeleton className="h-16" />
              <Skeleton className="h-16" />
            </div>
          )}
          <ul className="divide-y divide-line">
            {rows.map((a, i) => {
              const k = keyOf(a)
              return (
                <li key={k} className={cx('flex gap-3 px-4 py-3', selected.has(k) && 'bg-accent/[.06]')}>
                  <input type="checkbox" className={cx(checkbox, 'mt-1 shrink-0')} checked={selected.has(k)} onClick={(e) => toggle(i, e.shiftKey)} onChange={() => {}} aria-label="选择" />
                  <button className="min-w-0 flex-1 text-left" onClick={() => setOpenKey(k)}>
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0 text-[13.5px]">
                        <Label a={a} />
                      </div>
                      <StatusBadge status={a.status} />
                    </div>
                    <div className="mt-2 grid grid-cols-2 gap-3 text-[12px]">
                      <div>
                        <QuotaCell a={a} />
                      </div>
                      <div className="text-right">
                        <ExpiryCell a={a} />
                      </div>
                    </div>
                  </button>
                </li>
              )
            })}
          </ul>
        </div>

        {!isLoading && !rows.length && (
          <Empty
            icon={<Users size={28} />}
            title={accounts?.length ? '没有匹配的账号' : '还没有账号'}
            hint={accounts?.length ? '调整筛选条件试试。' : '通过授权登录或导入凭证，把账号加入池中。'}
            action={
              accounts?.length ? (
                <Button
                  onClick={() => {
                    setQ('')
                    setParam('status', '')
                  }}
                >
                  清除筛选
                </Button>
              ) : (
                <Button variant="primary" icon={<Plus size={15} />} onClick={() => setAdding(true)}>
                  添加账号
                </Button>
              )
            }
          />
        )}
        {rows.length > 0 && (
          <div className="border-t border-line px-5 py-2.5 text-[12px] text-faint">
            {rows.length === accounts?.length ? `共 ${rows.length} 个账号` : `显示 ${rows.length} / ${accounts?.length ?? 0} 个账号`}
            <span className="hidden md:inline"> · 按住 Shift 可连续选择</span>
          </div>
        )}
      </Card>

      {selectedRows.length > 0 && (
        <BulkBar
          rows={selectedRows}
          onKeep={(pid) => setSelected(new Set(selectedRows.filter((a) => a.provider === pid).map(keyOf)))}
          onExport={() => setExporting({ open: true, keys: selectedRows.map(keyOf) })}
          onClear={() => {
            setSelected(new Set())
            lastIdx.current = null
          }}
        />
      )}
      <AccountDrawer account={open} onClose={() => setOpenKey(null)} />
      <AddAccountDialog open={adding} onClose={() => setAdding(false)} defaultProvider={provider || undefined} />
      <ExportAccountsDialog open={exporting.open} onClose={() => setExporting((s) => ({ ...s, open: false }))} accounts={accounts ?? []} preselected={exporting.keys} />
    </div>
  )
}
