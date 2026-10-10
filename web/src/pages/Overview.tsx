import { AlertTriangle, ArrowUpRight, Hourglass, Layers } from 'lucide-react'
import { Link } from 'react-router-dom'
import { useAccounts, useOverview } from '@/lib/queries'
import { fmtNum, fmtRelative, pct } from '@/lib/format'
import { Badge, Card, CardHeader, Empty, Meter, Skeleton, cx } from '@/components/ui'

function LedgerStat({ label, value, unit, foot, tone, delay }: { label: string; value: string; unit?: string; foot?: string; tone?: string; delay: number }) {
  return (
    <div className="animate-rise px-6 py-5" style={{ animationDelay: `${delay}ms` }}>
      <div className="eyebrow">{label}</div>
      <div className={cx('mt-2 flex items-baseline gap-1.5 font-display text-[34px] font-semibold leading-none tracking-tight', tone)}>
        <span className="num">{value}</span>
        {unit && <span className="text-[13px] font-normal text-faint">{unit}</span>}
      </div>
      {foot && <div className="mt-2 text-[12.5px] text-muted">{foot}</div>}
    </div>
  )
}

export function OverviewPage() {
  const { data: ov, isLoading } = useOverview()
  const { data: accounts } = useAccounts()
  const s = ov?.summary

  const now = Date.now()
  const expiring = (accounts ?? [])
    .flatMap((a) =>
      (a.quota?.packages ?? [])
        .filter((p) => p.expire_at && p.remaining > 0 && new Date(p.expire_at).getTime() > now)
        .map((p) => ({ ...p, account: a })),
    )
    .sort((a, b) => new Date(a.expire_at!).getTime() - new Date(b.expire_at!).getTime())
    .slice(0, 6)

  return (
    <div className="space-y-6">
      <Card className="overflow-hidden">
        {isLoading || !s ? (
          <div className="grid grid-cols-4 gap-6 p-6">
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-20" />
            ))}
          </div>
        ) : (
          <div className="grid grid-cols-1 divide-line sm:grid-cols-2 sm:divide-x lg:grid-cols-4">
            <LedgerStat delay={0} label="剩余积分" value={fmtNum(s.remaining)} unit={`/ ${fmtNum(s.quota)}`} foot={`池内余量 ${pct(s.remaining, s.quota).toFixed(0)}%`} />
            <LedgerStat delay={60} label="7 天内到期" value={fmtNum(s.expiring_7d)} unit="积分" tone={s.expiring_7d > 0 ? 'text-accent' : ''} foot="优先消耗，避免过期浪费" />
            <LedgerStat delay={120} label="可用账号" value={String(s.active)} unit={`/ ${s.total}`} foot={`暂停 ${s.paused} · 禁用 ${s.disabled}`} />
            <LedgerStat delay={180} label="冷却中" value={String(s.cooling)} tone={s.cooling > 0 ? 'text-warn' : ''} foot="到期自动恢复" />
          </div>
        )}
        <div className="hairline h-px" />
        <div className="flex items-center justify-between px-6 py-3 text-[12.5px] text-muted">
          <span>每 5 秒自动刷新 · 仅读内存快照，不打上游</span>
          <Link to="/credits" className="inline-flex items-center gap-1 font-medium text-ink hover:text-accent">
            积分中心 <ArrowUpRight size={14} />
          </Link>
        </div>
      </Card>

      <div className="grid gap-6 lg:grid-cols-5">
        <Card className="lg:col-span-3">
          <CardHeader title="平台" hint="每个平台独立的账号池与额度" />
          <div className="divide-y divide-line">
            {ov?.providers.map((p) => (
              <Link key={p.id} to={`/accounts?provider=${p.id}`} className="grid grid-cols-[1fr_auto] items-center gap-4 px-5 py-3.5 hover:bg-sunken/50">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <Layers size={15} className="text-faint" />
                    <span className="font-medium">{p.name}</span>
                    <Badge>{p.total} 个账号</Badge>
                    {p.cooling > 0 && <Badge tone="warn">{p.cooling} 冷却</Badge>}
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

        <Card className="lg:col-span-2">
          <CardHeader title="即将过期" hint="按到期时间排序的积分包" actions={<Hourglass size={16} className="text-accent" />} />
          <ul className="px-2 pb-3">
            {expiring.map((p, i) => {
              const hours = (new Date(p.expire_at!).getTime() - now) / 3600_000
              return (
                <li key={i} className="flex items-center justify-between gap-3 rounded-lg px-3 py-2.5 hover:bg-sunken/50">
                  <div className="min-w-0">
                    <div className="truncate text-[13.5px] font-medium">{p.account.label}</div>
                    <div className="truncate text-[12px] text-faint">{p.name ?? p.source ?? '积分包'}</div>
                  </div>
                  <div className="text-right">
                    <div className="num text-[14px] font-semibold">{fmtNum(p.remaining)}</div>
                    <div className={cx('flex items-center justify-end gap-1 text-[11.5px]', hours < 48 ? 'text-bad' : 'text-muted')}>
                      {hours < 48 && <AlertTriangle size={11} />}
                      {fmtRelative(p.expire_at)}
                    </div>
                  </div>
                </li>
              )
            })}
            {!expiring.length && <Empty title="没有待过期积分" hint="积分包到期信息会在这里汇总。" />}
          </ul>
        </Card>
      </div>
    </div>
  )
}
