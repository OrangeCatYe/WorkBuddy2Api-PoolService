import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { Activity, AlertTriangle, CalendarX2, ArrowUpRight, CheckCircle2, Clock3, Coins, Flame, Hourglass, Layers, ShieldAlert, ScrollText } from 'lucide-react'
import { useAccounts, useCredits, useLatestRuns, useOverview, useRequests, useUsageStats } from '@/lib/queries'
import { fmtNum, fmtRelative, fmtTokens, pct } from '@/lib/format'
import { Badge, Card, CardHeader, Empty, Meter, Skeleton, cx } from '@/components/ui'
import type { Account, CreditsSnapshot, Overview, TaskRun, UsageStats } from '@/types'

// ---------------- 顶部指标 ----------------

function Stat({ label, value, unit, foot, tone, to, delay }: { label: string; value: string; unit?: string; foot?: React.ReactNode; tone?: string; to?: string; delay: number }) {
  const body = (
    <div className="group animate-rise px-5 py-5 sm:px-6" style={{ animationDelay: `${delay}ms` }}>
      <div className="eyebrow flex items-center justify-between">
        {label}
        {to && <ArrowUpRight size={13} className="opacity-0 transition-opacity group-hover:opacity-100" />}
      </div>
      <div className={cx('mt-2 flex items-baseline gap-1.5 font-display text-[30px] font-semibold leading-none tracking-tight sm:text-[34px]', tone)}>
        <span className="num">{value}</span>
        {unit && <span className="text-[13px] font-normal text-faint">{unit}</span>}
      </div>
      {foot && <div className="mt-2 text-[12.5px] text-muted">{foot}</div>}
    </div>
  )
  return to ? (
    <Link to={to} className="block bg-surface transition-colors hover:bg-sunken/40">
      {body}
    </Link>
  ) : (
    <div className="bg-surface">{body}</div>
  )
}

function Ledger({ ov, cr }: { ov?: Overview; cr?: CreditsSnapshot }) {
  const s = ov?.summary
  if (!s) {
    return (
      <div className="grid grid-cols-2 gap-6 p-6 lg:grid-cols-4">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-20" />
        ))}
      </div>
    )
  }
  const p = cr?.pool
  return (
    <div className="grid grid-cols-2 gap-px bg-line lg:grid-cols-4">
      <Stat delay={0} label="剩余积分" value={fmtNum(s.remaining)} unit={`/ ${fmtNum(s.quota)}`} foot={p ? `今日已用 ${fmtNum(p.today_credit)}` : `池内余量 ${pct(s.remaining, s.quota).toFixed(0)}%`} to="/credits" />
      <Stat
        delay={60}
        label="7 天内到期"
        value={fmtNum(p?.expiring_7d ?? s.expiring_7d)}
        tone={(p?.expiring_72h ?? 0) > 0 ? 'text-warn' : (p?.expiring_7d ?? s.expiring_7d) > 0 ? 'text-accent' : ''}
        foot={p?.next_expire_at ? `最近一批 ${fmtRelative(p.next_expire_at)}` : '暂无到期积分'}
        to="/credits"
      />
      <Stat
        delay={120}
        label="预计浪费"
        value={p ? fmtNum(p.waste) : '—'}
        tone={p && p.waste >= 1 ? 'text-bad' : 'text-ok'}
        foot={p ? (p.required_per_day > p.burn_per_day + 0.5 ? `零浪费需每天 ${fmtNum(Math.ceil(p.required_per_day))}` : '按当前速度可用完') : '计算中'}
        to="/credits"
      />
      <Stat delay={180} label="可用账号" value={String(s.active)} unit={`/ ${s.total}`} tone={s.total > 0 && s.active === 0 ? 'text-bad' : ''} foot={`冷却 ${s.cooling} · 暂停 ${s.paused} · 禁用 ${s.disabled}`} to="/accounts" />
    </div>
  )
}

// ---------------- 需要关注 ----------------

type Alert = { id: string; tone: 'bad' | 'warn' | 'info'; icon: React.ReactNode; title: string; hint?: string; to: string }

function buildAlerts(accounts: Account[] | undefined, cr: CreditsSnapshot | undefined, usage: UsageStats | undefined, runs?: Record<string, TaskRun>): Alert[] {
  const out: Alert[] = []
  // 24 小时内结束、且有账号失败的任务（取每个任务最近一次）
  Object.values(runs ?? {})
    .filter((r) => r.ended_at && (r.status === 'failed' || r.status === 'partial') && Date.now() - new Date(r.ended_at).getTime() < 86400_000)
    .forEach((r) =>
      out.push({
        id: `run-${r.provider}/${r.task}`,
        tone: r.status === 'failed' ? 'bad' : 'warn',
        icon: <CalendarX2 size={15} />,
        title: `${r.task_name}：${r.summary.failed} 个账号失败`,
        hint: `最近一次 ${fmtRelative(r.started_at)}执行，成功 ${r.summary.ok}、跳过 ${r.summary.skipped}`,
        to: '/tasks',
      }),
    )
  const acc = accounts ?? []
  const broken = acc.filter((a) => a.status === 'error' || a.status === 'disabled')
  if (broken.length) {
    out.push({
      id: 'broken',
      tone: 'bad',
      icon: <ShieldAlert size={15} />,
      title: `${broken.length} 个账号已禁用或异常`,
      hint: broken
        .slice(0, 3)
        .map((a) => `${a.label || a.id.slice(0, 8)}${a.status_reason ? `：${a.status_reason}` : ''}`)
        .join('；'),
      to: '/accounts?status=disabled',
    })
  }
  const cooling = acc.filter((a) => a.status === 'cooling')
  if (cooling.length) {
    const soonest = cooling.map((a) => a.cooling_until).filter(Boolean).sort()[0]
    out.push({ id: 'cooling', tone: 'warn', icon: <Clock3 size={15} />, title: `${cooling.length} 个账号冷却中`, hint: soonest ? `最早 ${fmtRelative(soonest)}恢复` : undefined, to: '/accounts?status=cooling' })
  }
  if (acc.length && !acc.some((a) => a.status === 'active')) {
    out.unshift({ id: 'none', tone: 'bad', icon: <AlertTriangle size={15} />, title: '没有可用账号', hint: '网关请求会返回 503，直到有账号恢复', to: '/accounts' })
  }
  const p = cr?.pool
  if (p && p.expiring_24h >= 1) {
    out.push({ id: 'exp24', tone: 'bad', icon: <Hourglass size={15} />, title: `${fmtNum(p.expiring_24h)} 积分将在 24 小时内到期`, hint: '收费请求会优先分给这些账号', to: '/credits' })
  } else if (p && p.expiring_72h >= 1) {
    out.push({ id: 'exp72', tone: 'warn', icon: <Hourglass size={15} />, title: `${fmtNum(p.expiring_72h)} 积分将在 3 天内到期`, hint: '收费请求会优先分给这些账号', to: '/credits' })
  }
  if (p && p.waste >= 1 && p.required_per_day > p.burn_per_day) {
    out.push({
      id: 'waste',
      tone: 'warn',
      icon: <Flame size={15} />,
      title: `按当前速度会浪费 ${fmtNum(p.waste)} 积分`,
      hint: `日均 ${fmtNum(Math.round(p.burn_per_day))}，零浪费需要 ${fmtNum(Math.ceil(p.required_per_day))}`,
      to: '/credits',
    })
  }
  const t = usage?.total
  if (t && t.requests >= 10 && t.failures / t.requests >= 0.1) {
    out.push({ id: 'fail', tone: 'warn', icon: <AlertTriangle size={15} />, title: `近 24 小时失败率 ${((t.failures / t.requests) * 100).toFixed(0)}%`, hint: `${t.failures} / ${t.requests} 个请求失败`, to: '/logs?status=error' })
  }
  return out
}

function Attention({ alerts }: { alerts: Alert[] }) {
  const toneCls = { bad: 'text-bad bg-bad/10', warn: 'text-warn bg-warn/12', info: 'text-info bg-info/10' }
  return (
    <Card className="flex flex-col">
      <CardHeader title="需要关注" hint={alerts.length ? `${alerts.length} 项` : '一切正常'} />
      {alerts.length ? (
        <ul className="flex-1 space-y-1 px-2 pb-3">
          {alerts.map((a) => (
            <li key={a.id}>
              <Link to={a.to} className="group flex items-start gap-3 rounded-xl px-3 py-2.5 hover:bg-sunken/60">
                <span className={cx('mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-lg', toneCls[a.tone])}>{a.icon}</span>
                <div className="min-w-0 flex-1">
                  <div className="text-[13.5px] font-medium">{a.title}</div>
                  {a.hint && <div className="mt-0.5 line-clamp-2 text-[12px] text-muted">{a.hint}</div>}
                </div>
                <ArrowUpRight size={14} className="mt-1 shrink-0 text-faint opacity-0 transition-opacity group-hover:opacity-100" />
              </Link>
            </li>
          ))}
        </ul>
      ) : (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 pb-8 pt-2 text-center">
          <CheckCircle2 size={26} className="text-ok" />
          <p className="text-[13px] text-muted">账号、积分与请求都没有异常</p>
        </div>
      )}
    </Card>
  )
}

// ---------------- 近 24 小时 ----------------

function Traffic({ u }: { u?: UsageStats }) {
  const t = u?.total
  const series = u?.series ?? []
  const max = Math.max(1, ...series.map((b) => b.requests))
  const okRate = t?.requests ? ((t.requests - t.failures) / t.requests) * 100 : 0
  const cacheRate = t?.prompt_tokens ? (t.cached_tokens / t.prompt_tokens) * 100 : 0
  return (
    <Card className="flex flex-col">
      <CardHeader
        title="近 24 小时"
        hint="经过网关的请求"
        actions={
          <Link to="/usage" className="inline-flex items-center gap-1 text-[12.5px] font-medium text-muted hover:text-accent">
            用量分析 <ArrowUpRight size={13} />
          </Link>
        }
      />
      {!u ? (
        <div className="px-5 pb-5">
          <Skeleton className="h-36" />
        </div>
      ) : !t?.requests ? (
        <Empty icon={<Activity size={24} />} title="还没有请求" hint="在 Agent 中接入网关后，这里会显示请求量与消耗。" />
      ) : (
        <div className="flex flex-1 flex-col px-5 pb-4">
          <div className="grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-4">
            {[
              ['请求', fmtNum(t.requests), `成功率 ${okRate.toFixed(okRate === 100 ? 0 : 1)}%`],
              ['消耗积分', fmtNum(Math.round(t.credit * 100) / 100), t.requests ? `平均 ${(t.credit / t.requests).toFixed(2)} / 次` : ''],
              ['Token', fmtTokens(t.prompt_tokens + t.completion_tokens), `缓存命中 ${cacheRate.toFixed(0)}%`],
              ['首字延迟', t.avg_ttfb_ms ? `${(t.avg_ttfb_ms / 1000).toFixed(1)}s` : '—', '平均'],
            ].map(([k, v, s]) => (
              <div key={k}>
                <div className="eyebrow">{k}</div>
                <div className="num mt-0.5 font-display text-[20px] font-semibold">{v}</div>
                <div className="text-[11.5px] text-faint">{s}</div>
              </div>
            ))}
          </div>
          <div className="mt-4 flex h-16 items-end gap-[2px]" aria-label="每小时请求数">
            {series.map((b, i) => (
              <div key={b.key || i} className="flex h-full flex-1 flex-col justify-end" title={`${b.key} · ${b.requests} 次${b.failures ? `，失败 ${b.failures}` : ''}`}>
                {b.requests > 0 && (
                  <div className="flex w-full flex-col overflow-hidden rounded-[3px]" style={{ height: `${Math.max(6, (b.requests / max) * 100)}%` }}>
                    {b.failures > 0 && <div className="w-full bg-bad/80" style={{ height: `${(b.failures / b.requests) * 100}%` }} />}
                    <div className="w-full flex-1 bg-accent/75" />
                  </div>
                )}
              </div>
            ))}
          </div>
          <div className="mt-1 flex justify-between text-[10.5px] text-faint">
            <span>24 小时前</span>
            <span>现在</span>
          </div>
        </div>
      )}
    </Card>
  )
}

// ---------------- 平台 / 到期 / 最近请求 ----------------

function Platforms({ ov }: { ov?: Overview }) {
  return (
    <Card>
      <CardHeader title="平台" hint="每个平台独立的账号池与额度" />
      <div className="divide-y divide-line border-t border-line">
        {ov?.providers.map((p) => (
          <Link key={p.id} to={`/accounts?provider=${p.id}`} className="grid grid-cols-[1fr_auto] items-center gap-4 px-5 py-3.5 hover:bg-sunken/50">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <Layers size={15} className="text-faint" />
                <span className="font-medium">{p.name}</span>
                <Badge>
                  {p.active}/{p.total} 可用
                </Badge>
                {p.cooling > 0 && <Badge tone="warn">{p.cooling} 冷却</Badge>}
                {p.disabled > 0 && <Badge tone="bad">{p.disabled} 禁用</Badge>}
              </div>
              <Meter className="mt-2.5" value={pct(p.remaining, p.quota)} />
            </div>
            <div className="text-right">
              <div className="num text-[15px] font-semibold">{fmtNum(p.remaining)}</div>
              <div className="text-[11.5px] text-faint">/ {fmtNum(p.quota)}</div>
            </div>
          </Link>
        ))}
        {!ov?.providers.length && <Empty icon={<Layers size={28} />} title="还没有启用平台" hint="在设置中启用 WorkBuddy 等平台后，这里会显示各平台的账号池。" />}
      </div>
    </Card>
  )
}

function Expiring({ cr }: { cr?: CreditsSnapshot }) {
  const now = Date.now()
  const rows = useMemo(
    () =>
      (cr?.accounts ?? [])
        .flatMap((a) => (a.packages ?? []).filter((p) => p.expire_at && new Date(p.expire_at).getTime() > now).map((p) => ({ ...p, label: a.label })))
        .sort((a, b) => new Date(a.expire_at!).getTime() - new Date(b.expire_at!).getTime())
        .slice(0, 6),
    [cr, now],
  )
  return (
    <Card>
      <CardHeader
        title="近期到期"
        hint="按到期时间排序的积分包"
        actions={
          <Link to="/credits" className="inline-flex items-center gap-1 text-[12.5px] font-medium text-muted hover:text-accent">
            积分中心 <ArrowUpRight size={13} />
          </Link>
        }
      />
      <ul className="px-2 pb-3">
        {rows.map((p, i) => {
          const hours = (new Date(p.expire_at!).getTime() - now) / 3600_000
          return (
            <li key={i} className="flex items-center justify-between gap-3 rounded-lg px-3 py-2 hover:bg-sunken/50">
              <div className="min-w-0">
                <div className="truncate text-[13.5px] font-medium">{p.label}</div>
                <div className="truncate text-[12px] text-faint">{p.name || p.source || '积分包'}</div>
              </div>
              <div className="shrink-0 text-right">
                <div className="num text-[14px] font-semibold">
                  {fmtNum(p.remaining)}
                  {p.waste >= 1 && <span className="ml-1 text-[11.5px] font-normal text-bad">浪费 {fmtNum(p.waste)}</span>}
                </div>
                <div className={cx('flex items-center justify-end gap-1 text-[11.5px]', hours < 48 ? 'text-bad' : 'text-muted')}>
                  {hours < 48 && <AlertTriangle size={11} />}
                  {fmtRelative(p.expire_at)}
                </div>
              </div>
            </li>
          )
        })}
        {!rows.length && <Empty icon={<Coins size={24} />} title="没有待过期积分" hint="积分包到期信息会在这里汇总。" />}
      </ul>
    </Card>
  )
}

function Recent() {
  const { data } = useRequests({ limit: '6' }, true)
  return (
    <Card>
      <CardHeader
        title="最近请求"
        actions={
          <Link to="/logs" className="inline-flex items-center gap-1 text-[12.5px] font-medium text-muted hover:text-accent">
            请求日志 <ArrowUpRight size={13} />
          </Link>
        }
      />
      <ul className="divide-y divide-line border-t border-line">
        {data?.map((r) => (
          <li key={r.id} className="flex items-center gap-3 px-5 py-2.5 text-[13px]">
            <span className={cx('h-1.5 w-1.5 shrink-0 rounded-full', r.ok ? 'bg-ok' : 'bg-bad')} />
            <div className="min-w-0 flex-1">
              <div className="num truncate font-medium">{r.requested_model || r.model || '—'}</div>
              <div className="truncate text-[11.5px] text-faint">
                {r.ok ? `${fmtTokens(r.prompt_tokens)} → ${fmtTokens(r.completion_tokens)}` : r.error_kind || `HTTP ${r.status}`}
                {r.key_name && ` · ${r.key_name}`}
              </div>
            </div>
            <div className="shrink-0 text-right">
              <div className="num text-[12.5px]">{r.has_credit ? fmtNum(r.credit) : '—'}</div>
              <div className="text-[11px] text-faint">{fmtRelative(r.time)}</div>
            </div>
          </li>
        ))}
        {data && !data.length && <Empty icon={<ScrollText size={24} />} title="暂无请求" />}
      </ul>
    </Card>
  )
}

export function OverviewPage() {
  const { data: ov } = useOverview()
  const { data: cr } = useCredits()
  const { data: accounts } = useAccounts()
  const { data: usage } = useUsageStats(24)
  const { data: runs } = useLatestRuns()
  const alerts = buildAlerts(accounts, cr, usage, runs)

  return (
    <div className="space-y-6">
      <Card className="overflow-hidden">
        <Ledger ov={ov} cr={cr} />
      </Card>
      <div className="grid gap-6 lg:grid-cols-5">
        <div className="lg:col-span-2">
          <Attention alerts={alerts} />
        </div>
        <div className="lg:col-span-3">
          <Traffic u={usage} />
        </div>
      </div>
      <div className="grid gap-6 lg:grid-cols-3">
        <Platforms ov={ov} />
        <Expiring cr={cr} />
        <Recent />
      </div>
    </div>
  )
}
