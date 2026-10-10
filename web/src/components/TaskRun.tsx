import { CheckCircle2, CircleDashed, Loader2, MinusCircle, XCircle } from 'lucide-react'
import { useTaskRun } from '@/lib/queries'
import { fmtDate, fmtNum, fmtRelative } from '@/lib/format'
import type { RunItemStatus, RunStatus, TaskRun, TaskRunItem } from '@/types'
import { Drawer } from './overlay'
import { Badge, Skeleton, cx } from './ui'

const runTone: Record<RunStatus, { tone: 'ok' | 'warn' | 'bad' | 'info' | 'neutral'; label: string }> = {
  running: { tone: 'info', label: '执行中' },
  ok: { tone: 'ok', label: '成功' },
  partial: { tone: 'warn', label: '部分失败' },
  failed: { tone: 'bad', label: '失败' },
  skipped: { tone: 'neutral', label: '无操作' },
}

export function RunStatusBadge({ status }: { status: RunStatus }) {
  const s = runTone[status] ?? runTone.skipped
  return (
    <Badge tone={s.tone}>
      {status === 'running' && <Loader2 size={11} className="animate-spin" />}
      {s.label}
    </Badge>
  )
}

const itemIcon: Record<RunItemStatus, React.ReactNode> = {
  pending: <CircleDashed size={14} className="text-faint" />,
  running: <Loader2 size={14} className="animate-spin text-info" />,
  ok: <CheckCircle2 size={14} className="text-ok" />,
  skipped: <MinusCircle size={14} className="text-faint" />,
  error: <XCircle size={14} className="text-bad" />,
}

export const triggerLabel = (t: TaskRun['trigger']) => (t === 'schedule' ? '定时' : '手动')

export function duration(r: Pick<TaskRun, 'started_at' | 'ended_at'>) {
  const ms = (r.ended_at ? new Date(r.ended_at).getTime() : Date.now()) - new Date(r.started_at).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)}s`
  return `${Math.floor(ms / 60_000)} 分 ${Math.round((ms % 60_000) / 1000)} 秒`
}

/** 奖励文案：+120 积分 · +5 能量 */
export function rewardText(credit?: number, energy?: number) {
  const parts: string[] = []
  if (credit) parts.push(`+${fmtNum(credit)} 积分`)
  if (energy) parts.push(`+${fmtNum(energy)} 能量`)
  return parts.join(' · ')
}

/** 执行汇总条：成功 / 跳过 / 失败的比例。 */
export function SummaryBar({ s, className }: { s: TaskRun['summary']; className?: string }) {
  if (!s.total) return <div className={cx('h-1.5 rounded-full bg-sunken', className)} />
  const seg = (n: number) => `${(n / s.total) * 100}%`
  return (
    <div className={cx('flex h-1.5 overflow-hidden rounded-full bg-sunken', className)} title={`成功 ${s.ok} · 跳过 ${s.skipped} · 失败 ${s.failed}${s.pending ? ` · 待执行 ${s.pending}` : ''}`}>
      <div className="bg-ok" style={{ width: seg(s.ok) }} />
      <div className="bg-faint/50" style={{ width: seg(s.skipped) }} />
      <div className="bg-bad" style={{ width: seg(s.failed) }} />
    </div>
  )
}

export function RunItems({ items, showAccount = true }: { items: TaskRunItem[]; showAccount?: boolean }) {
  if (!items.length) return null
  return (
    <ul className="divide-y divide-line rounded-xl border border-line">
      {items.map((it, i) => (
        <li key={i} className={cx('flex gap-2.5 px-3.5 py-2.5', it.status === 'running' && 'bg-info/[.05]')}>
          <span className="mt-[3px] shrink-0">{itemIcon[it.status] ?? itemIcon.pending}</span>
          <div className="min-w-0 flex-1">
            <div className="flex items-baseline justify-between gap-2">
              <span className="truncate text-[13px] font-medium">
                {showAccount ? it.account || it.account_id.slice(0, 10) : it.title || it.code}
                {showAccount && (it.title || it.code) && <span className="font-normal text-muted"> · {it.title || it.code}</span>}
              </span>
              {(it.credit || it.energy) && <span className="num shrink-0 text-[12px] font-medium text-ok">{rewardText(it.credit, it.energy)}</span>}
            </div>
            {it.message ? (
              <p className={cx('mt-0.5 break-words text-[12px]', it.status === 'error' ? 'text-bad' : 'text-muted')}>{it.message}</p>
            ) : (
              it.status === 'pending' && <p className="mt-0.5 text-[12px] text-faint">等待执行</p>
            )}
          </div>
        </li>
      ))}
    </ul>
  )
}

export function RunDrawer({ id, onClose }: { id: string | null; onClose: () => void }) {
  const { data: run, isLoading } = useTaskRun(id)
  if (!id) return null
  const s = run?.summary
  return (
    <Drawer
      open
      onClose={onClose}
      title={run?.task_name ?? '执行详情'}
      subtitle={run && <span className="num">{run.id}</span>}
    >
      {isLoading || !run || !s ? (
        <Skeleton className="h-40" />
      ) : (
        <div className="space-y-5">
          <div className="flex flex-wrap items-center gap-2">
            <RunStatusBadge status={run.status} />
            <Badge>{triggerLabel(run.trigger)}</Badge>
            <Badge>{run.scope ? `${run.scope} 个账号` : '全部账号'}</Badge>
          </div>
          <div className="grid grid-cols-3 gap-3 rounded-xl bg-sunken/60 p-3.5 text-[12.5px]">
            <div>
              <div className="eyebrow">开始</div>
              <div className="num mt-0.5">{fmtDate(run.started_at)}</div>
            </div>
            <div>
              <div className="eyebrow">耗时</div>
              <div className="num mt-0.5">{duration(run)}</div>
            </div>
            <div>
              <div className="eyebrow">奖励</div>
              <div className="num mt-0.5 text-ok">{rewardText(s.credit, s.energy) || '—'}</div>
            </div>
          </div>
          <div>
            <div className="mb-1.5 flex justify-between text-[12px] text-muted">
              <span>
                成功 <b className="num text-ok">{s.ok}</b> · 跳过 <b className="num">{s.skipped}</b> · 失败 <b className={cx('num', s.failed && 'text-bad')}>{s.failed}</b>
              </span>
              {s.pending > 0 && <span className="num">剩余 {s.pending}</span>}
            </div>
            <SummaryBar s={s} />
          </div>
          {run.note && <p className="rounded-lg bg-warn/[.08] px-3 py-2 text-[12.5px] text-muted">{run.note}</p>}
          <RunItems items={run.items ?? []} />
          {!run.items?.length && !run.note && <p className="text-[13px] text-faint">没有处理任何账号{run.ended_at ? '' : '（仍在扫描）'}</p>}
          {!run.ended_at && <p className="text-[12px] text-faint">执行中，每 2 秒自动刷新 · 开始于 {fmtRelative(run.started_at)}</p>}
        </div>
      )}
    </Drawer>
  )
}
