import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, Coins, Pause, Play, RefreshCw, Route, Sparkles } from 'lucide-react'
import { api, post } from '@/lib/api'
import { useCredits, useDecisions } from '@/lib/queries'
import { fmtDate, fmtNum, fmtRelative, pct } from '@/lib/format'
import { Drawer, Segmented, useToast } from '@/components/overlay'
import { Badge, Button, Card, CardHeader, Empty, Meter, Skeleton, StatusBadge, cx } from '@/components/ui'
import type { AccountForecast, CreditsSnapshot, CreditsStrategy, Decision } from '@/types'

const fmtDays = (iso?: string) => {
  if (!iso) return '—'
  const d = (new Date(iso).getTime() - Date.now()) / 86400_000
  if (d < 1) return `${Math.max(1, Math.round(d * 24))} 小时`
  return d >= 365 ? '一年以上' : `${d < 10 ? d.toFixed(1) : Math.round(d)} 天`
}

// ---------------- 顶部指标 ----------------

function Kpi({ label, value, sub, tone, children }: { label: string; value: string; sub?: React.ReactNode; tone?: 'bad' | 'warn' | 'ok'; children?: React.ReactNode }) {
  return (
    <Card className="px-4 py-3.5 sm:px-5 sm:py-4">
      <div className="eyebrow">{label}</div>
      <div className={cx('num mt-1 font-display text-[22px] sm:text-[26px] font-semibold leading-none', tone === 'bad' && 'text-bad', tone === 'warn' && 'text-warn', tone === 'ok' && 'text-ok')}>{value}</div>
      {sub && <div className="mt-1.5 text-[12px] text-muted">{sub}</div>}
      {children}
    </Card>
  )
}

function Summary({ s }: { s: CreditsSnapshot }) {
  const p = s.pool
  const saved = p.waste - p.ideal_waste
  const pace = p.required_per_day > 0 ? pct(p.burn_per_day, p.required_per_day) : 100
  return (
    <div className="grid grid-cols-2 gap-3 sm:gap-4 xl:grid-cols-4">
      <Kpi label="可用积分" value={fmtNum(p.remaining)} sub={`${p.accounts} 个账号 · 今日已用 ${fmtNum(p.today_credit)}`} />
      <Kpi
        label="7 天内到期"
        value={fmtNum(p.expiring_7d)}
        tone={p.expiring_72h > 0 ? 'warn' : undefined}
        sub={p.next_expire_at ? `最近一批 ${fmtDays(p.next_expire_at)}后到期 · 3 天内 ${fmtNum(p.expiring_72h)}` : '暂无到期积分'}
      />
      <Kpi
        label="预计浪费"
        value={fmtNum(p.waste)}
        tone={p.waste >= 1 ? 'bad' : 'ok'}
        sub={saved >= 1 ? `按到期统一调度可降到 ${fmtNum(p.ideal_waste)}` : p.waste >= 1 ? '按当前速度，到期前用不完' : '按当前速度可全部用完'}
      />
      <Kpi label="日均消耗" value={fmtNum(Math.round(p.burn_per_day * 10) / 10)} sub={p.required_per_day > 0 ? `零浪费需要每天 ${fmtNum(Math.ceil(p.required_per_day))}` : '没有会过期的积分'}>
        {p.required_per_day > 0 && <Meter className="mt-2.5" value={pace} tone={pace >= 100 ? 'ok' : pace >= 60 ? 'warn' : 'bad'} />}
      </Kpi>
    </div>
  )
}

// ---------------- 到期日历 ----------------

function CalendarChart({ s }: { s: CreditsSnapshot }) {
  const [hover, setHover] = useState<number | null>(null)
  const days = s.calendar
  const max = Math.max(1, ...days.map((d) => d.expiring))
  const h = hover !== null ? days[hover] : null
  const label = (d: string) => {
    const [, m, dd] = d.split('-')
    return `${+m}/${+dd}`
  }
  return (
    <Card>
      <CardHeader
        title="到期日历"
        hint="未来 30 天每天到期的积分；红色部分按当前消耗速度到期前用不完"
        actions={
          <div className="num h-5 text-right text-[12px] text-muted">
            {h ? (
              <>
                {label(h.date)} · 到期 {fmtNum(h.expiring)}（{h.packages} 包）
                {h.waste >= 1 && <span className="text-bad"> · 浪费 {fmtNum(h.waste)}</span>}
              </>
            ) : (
              <>30 天合计 {fmtNum(s.pool.expiring_30d)}</>
            )}
          </div>
        }
      />
      <div className="px-5 pb-4">
        <div className="flex h-[150px] items-end gap-[3px]" onMouseLeave={() => setHover(null)}>
          {days.map((d, i) => {
            const total = (d.expiring / max) * 100
            const waste = d.expiring ? (d.waste / d.expiring) * total : 0
            return (
              <div key={d.date} className="relative flex h-full flex-1 cursor-default flex-col justify-end" onMouseEnter={() => setHover(i)}>
                <div className={cx('absolute inset-0 rounded-md transition-colors', hover === i && 'bg-sunken')} />
                {d.expiring > 0 && (
                  <div className="relative flex w-full flex-col overflow-hidden rounded-[4px]" style={{ height: `${Math.max(3, total)}%` }}>
                    <div className="w-full bg-bad/85" style={{ height: `${(waste / total) * 100}%` }} />
                    <div className={cx('w-full flex-1', hover === i ? 'bg-accent' : 'bg-accent/70')} />
                  </div>
                )}
              </div>
            )
          })}
        </div>
        <div className="mt-1.5 flex h-4 gap-[3px]">
          {days.map((d, i) => (
            <div key={d.date} className="num relative flex-1 text-[10.5px] text-faint">
              {i % 5 === 0 && <span className="absolute left-0 whitespace-nowrap">{i === 0 ? '今天' : label(d.date)}</span>}
            </div>
          ))}
        </div>
        <div className="mt-3 flex items-center gap-4 text-[11.5px] text-muted">
          <span className="flex items-center gap-1.5">
            <span className="h-2 w-2 rounded-sm bg-accent/70" /> 到期前可用完
          </span>
          <span className="flex items-center gap-1.5">
            <span className="h-2 w-2 rounded-sm bg-bad/85" /> 预计浪费
          </span>
          <span className="ml-auto text-faint">
            消耗速度取最近 {s.window_days} 天网关记录{s.history_since && ` · 数据起点 ${fmtDate(s.history_since)}`}
          </span>
        </div>
      </div>
    </Card>
  )
}

// ---------------- 账号预测 ----------------

function Spark({ data }: { data: number[] }) {
  const max = Math.max(...data)
  if (!max) return <span className="text-[11.5px] text-faint">无消耗</span>
  const W = 84
  const H = 22
  const pts = data.map((v, i) => `${(i / (data.length - 1)) * W},${H - (v / max) * (H - 2) - 1}`).join(' ')
  return (
    <svg width={W} height={H} className="overflow-visible">
      <polyline points={pts} fill="none" className="stroke-accent" strokeWidth={1.5} strokeLinejoin="round" />
      <circle cx={W} cy={H - (data[data.length - 1] / max) * (H - 2) - 1} r={2} className="fill-accent" />
    </svg>
  )
}

function UrgencyCell({ a }: { a: AccountForecast }) {
  const tone = a.urgency >= 0.66 ? 'bad' : a.urgency >= 0.33 ? 'warn' : 'ok'
  return (
    <div className="w-[130px]">
      <Meter value={a.urgency * 100} tone={tone} />
      <div className="mt-1 truncate text-[11.5px] text-muted" title={a.note}>
        {a.note}
      </div>
    </div>
  )
}

function PackagesDrawer({ a, onClose }: { a: AccountForecast; onClose: () => void }) {
  const pkgs = a.packages ?? []
  const max = Math.max(1, ...pkgs.map((p) => p.remaining))
  return (
    <Drawer open onClose={onClose} title={a.label} subtitle={`${a.provider} · ${a.account_id}`}>
      <div className="grid grid-cols-3 gap-2">
        {[
          ['余额', fmtNum(a.remaining)],
          ['日均消耗', fmtNum(Math.round(a.burn_per_day * 10) / 10)],
          ['预计浪费', fmtNum(a.waste)],
        ].map(([k, v]) => (
          <div key={k} className="rounded-xl bg-sunken/70 px-3 py-2.5">
            <div className="eyebrow">{k}</div>
            <div className="num mt-0.5 font-display text-[18px] font-semibold">{v}</div>
          </div>
        ))}
      </div>
      <p className="mt-3 text-[12.5px] text-muted">
        {a.burn_per_day > 0
          ? `按每天 ${fmtNum(Math.round(a.burn_per_day))} 的速度、先到期先扣的顺序模拟；${a.exhaust_at ? `约 ${fmtDays(a.exhaust_at)}后用完` : ''}`
          : '最近没有消耗记录，会过期的积分都将浪费。'}
        {a.required_per_day > 0 && ` 零浪费需要每天 ${fmtNum(Math.ceil(a.required_per_day))}。`}
      </p>
      <div className="eyebrow mb-1.5 mt-5">积分包（{pkgs.length}）</div>
      <ul className="space-y-1">
        {pkgs.map((p, i) => (
          <li key={i} className="rounded-lg px-2 py-1.5 hover:bg-sunken/60">
            <div className="flex items-baseline justify-between gap-3 text-[12.5px]">
              <span className="min-w-0 truncate">{p.name || p.source || '积分包'}</span>
              <span className="num shrink-0 text-muted">
                {fmtNum(p.remaining)}
                {p.amount > p.remaining && <span className="text-faint"> / {fmtNum(p.amount)}</span>}
              </span>
            </div>
            <div className="mt-1 flex items-center gap-2">
              <div className="flex h-1.5 flex-1 overflow-hidden rounded-full bg-sunken">
                <div className="h-full bg-accent/80" style={{ width: `${(p.used / max) * 100}%` }} />
                <div className="h-full bg-bad/80" style={{ width: `${(p.waste / max) * 100}%` }} />
              </div>
              <span className={cx('num w-[92px] shrink-0 text-right text-[11px]', p.waste >= 1 ? 'text-bad' : 'text-faint')}>
                {p.expire_at ? `${fmtDate(p.expire_at).slice(0, 5)} · ${fmtDays(p.expire_at)}` : '不过期'}
              </span>
            </div>
          </li>
        ))}
      </ul>
    </Drawer>
  )
}

function AccountsTable({ s }: { s: CreditsSnapshot }) {
  const [open, setOpen] = useState<AccountForecast | null>(null)
  return (
    <Card className="overflow-x-auto">
      <CardHeader title="账号预测" hint="按到期压力排序；点击查看每个积分包的预测" />
      <table className="w-full min-w-[760px] whitespace-nowrap text-left">
        <thead>
          <tr className="border-y border-line text-[12px] text-faint">
            <th className="py-2.5 pl-5 font-medium">账号</th>
            <th className="px-3 py-2.5 text-right font-medium">余额</th>
            <th className="px-3 py-2.5 font-medium">近 14 天消耗</th>
            <th className="px-3 py-2.5 text-right font-medium">日均 / 用完</th>
            <th className="px-3 py-2.5 text-right font-medium">最近到期</th>
            <th className="px-3 py-2.5 text-right font-medium">预计浪费</th>
            <th className="px-5 py-2.5 font-medium">到期压力</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {s.accounts.map((a) => (
            <tr key={a.provider + a.account_id} onClick={() => setOpen(a)} className="cursor-pointer text-[13px] hover:bg-sunken/40">
              <td className="py-2.5 pl-5">
                <div className="flex items-center gap-2">
                  <span className="max-w-[140px] truncate font-medium">{a.label}</span>
                  {a.status !== 'active' && <StatusBadge status={a.status} />}
                  {a.capped && <Badge tone="warn">已达日上限</Badge>}
                </div>
                <div className="num text-[11.5px] text-faint">
                  {a.provider} · {a.account_id.slice(0, 8)}
                </div>
              </td>
              <td className="num px-3 py-2.5 text-right">{fmtNum(a.remaining)}</td>
              <td className="px-3 py-2.5">
                <Spark data={a.series ?? []} />
              </td>
              <td className="num px-3 py-2.5 text-right">
                {fmtNum(Math.round(a.burn_per_day * 10) / 10)}
                <div className="text-[11.5px] text-faint">{a.exhaust_at ? `${fmtDays(a.exhaust_at)}后用完` : `今日 ${fmtNum(a.today_credit)}`}</div>
              </td>
              <td className="num px-3 py-2.5 text-right">
                {a.next_expire_at ? (
                  <>
                    {fmtNum(a.next_expire_amount)}
                    <div className={cx('text-[11.5px]', a.expiring_72h > 0 ? 'text-warn' : 'text-faint')}>{fmtDays(a.next_expire_at)}后</div>
                  </>
                ) : (
                  <span className="text-faint">—</span>
                )}
              </td>
              <td className={cx('num px-3 py-2.5 text-right', a.waste >= 1 ? 'text-bad' : 'text-faint')}>{a.waste >= 1 ? fmtNum(a.waste) : '0'}</td>
              <td className="px-5 py-2.5">
                <UrgencyCell a={a} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {!s.accounts.length && <Empty icon={<Coins size={28} />} title="还没有带积分的账号" />}
      {open && <PackagesDrawer a={open} onClose={() => setOpen(null)} />}
    </Card>
  )
}

// ---------------- 策略 ----------------

function Slider({ label, hint, value, min, max, step, unit, onChange, zeroText }: { label: string; hint: string; value: number; min: number; max: number; step: number; unit?: string; onChange: (v: number) => void; zeroText?: string }) {
  return (
    <label className="block">
      <div className="flex items-baseline justify-between">
        <span className="text-[13px] font-medium">{label}</span>
        <span className="num text-[13px] text-accent">{value === 0 && zeroText ? zeroText : `${value}${unit ?? ''}`}</span>
      </div>
      <input type="range" min={min} max={max} step={step} value={value} onChange={(e) => onChange(Number(e.target.value))} className="mt-1.5 w-full accent-[rgb(var(--accent))]" />
      <span className="block text-[11.5px] text-faint">{hint}</span>
    </label>
  )
}

function StrategyCard({ s }: { s: CreditsSnapshot }) {
  const [v, setV] = useState<CreditsStrategy>(s.strategy)
  const qc = useQueryClient()
  const toast = useToast()
  useEffect(() => setV(s.strategy), [s.strategy])
  const dirty = JSON.stringify(v) !== JSON.stringify(s.strategy)
  const save = useMutation({
    mutationFn: (x: CreditsStrategy) => api<CreditsSnapshot>('/api/credits/strategy', { method: 'PUT', body: JSON.stringify(x) }),
    onSuccess: (snap) => {
      qc.setQueryData(['credits'], snap)
      toast.ok('策略已生效')
    },
    onError: (e: Error) => toast.bad(e.message),
  })
  const tunable = s.tunable_providers ?? []
  const on = v.strategy === 'waste_first'

  return (
    <Card>
      <CardHeader
        title="选号策略"
        hint={tunable.length ? `作用于：${tunable.join('、')}` : '当前没有支持策略调优的平台'}
        actions={<Segmented value={v.strategy} onChange={(x) => setV({ ...v, strategy: x })} options={[{ value: 'waste_first', label: '到期优先' }, { value: 'off', label: '关闭' }]} />}
      />
      <div className="space-y-4 border-t border-line px-5 py-4">
        <p className="text-[12.5px] leading-relaxed text-muted">
          {on
            ? '收费请求优先分给积分快到期的账号：权重 = 原算法权重 × (1 + (放大倍数 − 1) × 到期压力)，高价模型再额外放大。免费模型不消耗积分，不参与调整。'
            : '沿用平台原算法：余额比例 + 闲置补偿加权随机，快过期积分 ×3。'}
        </p>
        <div className={cx('space-y-4 transition-opacity', !on && 'pointer-events-none opacity-40')}>
          <Slider label="放大倍数" hint="到期压力最高的账号被选中的权重倍数" value={v.waste_boost} min={1} max={10} step={0.5} unit="×" onChange={(x) => setV({ ...v, waste_boost: x })} />
          <Slider label="高价模型门槛" hint="倍率不低于该值的模型，额外向快到期账号集中" value={v.expensive_rate} min={0.1} max={2} step={0.05} unit="×" onChange={(x) => setV({ ...v, expensive_rate: x })} />
          <Slider label="单账号每日上限" hint="当天用量超过后降权（仍可兜底），用于控制节奏" value={v.daily_cap} min={0} max={2000} step={50} zeroText="不限" onChange={(x) => setV({ ...v, daily_cap: x })} />
        </div>
        <Slider label="余额保底" hint="低于该值的账号不再接收费模型，留给免费模型用" value={v.reserve} min={0} max={1000} step={10} zeroText="沿用平台设置" onChange={(x) => setV({ ...v, reserve: x })} />
        <Slider label="速度统计窗口" hint="用最近多少天的网关消耗计算日均速度" value={v.burn_window_days} min={1} max={30} step={1} unit=" 天" onChange={(x) => setV({ ...v, burn_window_days: x })} />
      </div>
      <div className="flex justify-end gap-2 border-t border-line px-5 py-3">
        <Button size="sm" disabled={!dirty} onClick={() => setV(s.strategy)}>
          还原
        </Button>
        <Button size="sm" variant="primary" disabled={!dirty || save.isPending} onClick={() => save.mutate(v)}>
          保存并生效
        </Button>
      </div>
    </Card>
  )
}

// ---------------- 决策回放 ----------------

function DecisionRow({ d }: { d: Decision }) {
  const [open, setOpen] = useState(false)
  const cands = d.candidates ?? []
  const t = new Date(d.time)
  return (
    <li className="px-5 py-2.5">
      <button className="flex w-full items-start gap-3 text-left" onClick={() => cands.length > 1 && setOpen(!open)}>
        <span className="num w-[58px] shrink-0 pt-0.5 text-[11.5px] text-faint">{t.toLocaleTimeString('zh-CN', { hour12: false })}</span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5 text-[13px]">
            <span className="num font-medium">{d.model || '—'}</span>
            {d.rate >= 0 && <Badge tone={d.rate === 0 ? 'ok' : d.rate >= 1 ? 'warn' : 'neutral'}>{d.rate === 0 ? '免费' : `x${d.rate}`}</Badge>}
            <span className="text-faint">→</span>
            <span className="font-medium">{d.chosen_label}</span>
            {d.fallback && <Badge tone="warn">兜底</Badge>}
            {d.explored && <Badge tone="info">探索</Badge>}
          </div>
          <div className="mt-0.5 truncate text-[12px] text-muted">{d.reason}</div>
        </div>
        {cands.length > 1 && (
          <span className="flex shrink-0 items-center gap-0.5 pt-0.5 text-[11.5px] text-faint">
            {cands.length} 候选 <ChevronDown size={13} className={cx('transition-transform', open && 'rotate-180')} />
          </span>
        )}
      </button>
      {open && (
        <ul className="ml-[70px] mt-2 space-y-1.5 rounded-xl bg-sunken/60 p-3">
          {cands.map((c) => (
            <li key={c.account_id}>
              <div className="flex items-baseline justify-between gap-2 text-[12px]">
                <span className={cx('truncate', c.account_id === d.chosen && 'font-semibold text-ink')}>
                  {c.label}
                  {c.account_id === d.chosen && ' ✓'}
                </span>
                <span className="num shrink-0 text-faint">
                  {c.bias !== 1 && <span className={c.bias > 1 ? 'text-accent' : 'text-bad'}>×{c.bias.toFixed(2)} · </span>}
                  {(c.share * 100).toFixed(0)}%
                </span>
              </div>
              <Meter className="mt-1" value={c.share * 100} tone={c.account_id === d.chosen ? 'accent' : 'ok'} />
            </li>
          ))}
        </ul>
      )}
    </li>
  )
}

function DecisionsCard() {
  const [live, setLive] = useState(true)
  const { data, isLoading } = useDecisions(live)
  return (
    <Card className="flex flex-col">
      <CardHeader
        title="选号回放"
        hint="每次选号的候选、权重和理由，包括会话粘性命中"
        actions={
          <Button size="sm" onClick={() => setLive(!live)} icon={live ? <Pause size={12} /> : <Play size={12} />}>
            {live ? '实时' : '已暂停'}
          </Button>
        }
      />
      <div className="max-h-[560px] flex-1 overflow-y-auto border-t border-line">
        {isLoading ? (
          <div className="p-5">
            <Skeleton className="h-10" />
          </div>
        ) : data?.length ? (
          <ul className="divide-y divide-line">
            {data.map((d, i) => (
              <DecisionRow key={d.time + i} d={d} />
            ))}
          </ul>
        ) : (
          <Empty icon={<Route size={26} />} title="还没有选号记录" hint="网关收到请求后，这里会显示每次选了哪个账号以及原因。" />
        )}
      </div>
    </Card>
  )
}

// ---------------- 页面 ----------------

export function CreditsPage() {
  const { data, isLoading } = useCredits()
  const qc = useQueryClient()
  const refresh = useMutation({
    mutationFn: () => post<CreditsSnapshot>('/api/credits/refresh'),
    onSuccess: (snap) => qc.setQueryData(['credits'], snap),
  })
  const insight = useMemo(() => {
    if (!data) return null
    const p = data.pool
    if (p.expiring_72h >= 1) return `${fmtNum(p.expiring_72h)} 积分将在 3 天内到期，收费请求会优先分给这些账号。`
    if (p.waste >= 1) return `按当前速度，未来会浪费 ${fmtNum(p.waste)} 积分；每天多用 ${fmtNum(Math.ceil(p.required_per_day - p.burn_per_day))} 即可避免。`
    return null
  }, [data])

  if (isLoading || !data) return <Skeleton className="h-72" />
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        {insight ? (
          <div className="flex items-center gap-2 rounded-xl border border-accent/30 bg-accent/[.07] px-3.5 py-2 text-[13px]">
            <Sparkles size={14} className="shrink-0 text-accent" />
            {insight}
          </div>
        ) : (
          <span />
        )}
        <div className="flex items-center gap-2 text-[12px] text-faint">
          更新于 {fmtRelative(data.updated_at)}
          <Button size="sm" variant="ghost" icon={<RefreshCw size={12} className={refresh.isPending ? 'animate-spin' : ''} />} onClick={() => refresh.mutate()}>
            重新计算
          </Button>
        </div>
      </div>
      <Summary s={data} />
      <CalendarChart s={data} />
      <AccountsTable s={data} />
      <div className="grid gap-6 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
        <StrategyCard s={data} />
        <DecisionsCard />
      </div>
    </div>
  )
}
