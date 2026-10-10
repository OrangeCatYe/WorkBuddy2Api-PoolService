import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { ArrowUpRight, CalendarClock, Clock, History, Loader2, Play } from 'lucide-react'
import { post } from '@/lib/api'
import { useLatestRuns, useProviderDetails, useTaskRuns } from '@/lib/queries'
import { fmtDate, fmtRelative } from '@/lib/format'
import { useToast } from '@/components/overlay'
import { Badge, Button, Card, CardHeader, Empty, Skeleton, cx } from '@/components/ui'
import { RunDrawer, RunStatusBadge, SummaryBar, duration, rewardText, triggerLabel } from '@/components/TaskRun'
import type { TaskDef, TaskRun } from '@/types'

/** 下一个整点时刻（本地时区）。 */
function nextFire(hours?: number[]): Date | null {
  if (!hours?.length) return null
  const now = new Date()
  for (let d = 0; d < 2; d++) {
    for (const h of [...hours].sort((a, b) => a - b)) {
      const t = new Date(now)
      t.setDate(now.getDate() + d)
      t.setHours(h, 0, 0, 0)
      if (t.getTime() > now.getTime()) return t
    }
  }
  return null
}

function nextText(t: TaskDef) {
  if (!t.enabled) return ''
  const n = nextFire(t.hours)
  if (!n) return t.hours?.length ? '' : '周期执行'
  const today = new Date().toDateString() === n.toDateString()
  return `${today ? '今天' : '明天'} ${String(n.getHours()).padStart(2, '0')}:00`
}

function runLine(r: TaskRun) {
  const s = r.summary
  if (r.status === 'running') return `执行中 · ${s.ok + s.skipped + s.failed}/${s.total || '…'}`
  if (!s.total) return r.note || '没有需要处理的账号'
  const parts = [`成功 ${s.ok}`]
  if (s.skipped) parts.push(`跳过 ${s.skipped}`)
  if (s.failed) parts.push(`失败 ${s.failed}`)
  return parts.join(' · ')
}

function TaskCard({ pid, t, last, onOpen }: { pid: string; t: TaskDef; last?: TaskRun; onOpen: (id: string) => void }) {
  const toast = useToast()
  const qc = useQueryClient()
  const [busy, setBusy] = useState(false)
  const running = last?.status === 'running'
  async function run() {
    setBusy(true)
    try {
      await post(`/api/providers/${pid}/tasks/${t.id}/run`)
      toast.ok(`${t.name}：已开始执行`)
      setTimeout(() => qc.invalidateQueries({ queryKey: ['task-runs'] }), 400)
    } catch (e) {
      toast.bad((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <li className="flex flex-col gap-3 bg-surface px-5 py-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className={cx('h-1.5 w-1.5 shrink-0 rounded-full', t.enabled ? 'bg-ok' : 'bg-faint')} />
            <span className="font-medium">{t.name}</span>
            {!t.enabled && <Badge>已关闭</Badge>}
          </div>
          {t.description && <p className="mt-1 text-[12.5px] text-muted">{t.description}</p>}
        </div>
        <Button size="sm" className="shrink-0 whitespace-nowrap" disabled={busy || running} onClick={run} icon={busy || running ? <Loader2 size={12} className="animate-spin" /> : <Play size={12} />}>
          {running ? '执行中' : '执行'}
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-faint">
        {t.hours?.length ? (
          <span className="flex items-center gap-1">
            <Clock size={11} />
            {t.hours.map((h) => `${String(h).padStart(2, '0')}:00`).join(' · ')}
          </span>
        ) : null}
        {nextText(t) && <span>下次 {nextText(t)}</span>}
      </div>
      {last ? (
        <button onClick={() => onOpen(last.id)} className="group -mx-2 rounded-lg px-2 py-1.5 text-left hover:bg-sunken/60">
          <div className="flex items-center justify-between gap-2 text-[12px]">
            <span className="flex min-w-0 items-center gap-1.5">
              <RunStatusBadge status={last.status} />
              <span className="truncate text-muted">{runLine(last)}</span>
            </span>
            <span className="shrink-0 text-faint group-hover:text-accent">
              {triggerLabel(last.trigger)} · {fmtRelative(last.started_at)}
            </span>
          </div>
          <SummaryBar s={last.summary} className="mt-2" />
          {(last.summary.credit > 0 || last.summary.energy > 0) && <div className="num mt-1.5 text-[11.5px] font-medium text-ok">{rewardText(last.summary.credit, last.summary.energy)}</div>}
        </button>
      ) : (
        <p className="text-[12px] text-faint">还没有执行记录</p>
      )}
    </li>
  )
}

const statusOpts = [
  ['', '全部结果'],
  ['ok', '成功'],
  ['partial', '部分失败'],
  ['failed', '失败'],
  ['skipped', '无操作'],
  ['running', '执行中'],
]

function RunHistory({ tasks, onOpen }: { tasks: { id: string; name: string }[]; onOpen: (id: string) => void }) {
  const [task, setTask] = useState('')
  const [status, setStatus] = useState('')
  const params = Object.fromEntries(Object.entries({ task, status, limit: '100' }).filter(([, v]) => v))
  const { data: runs, isLoading } = useTaskRuns(params)
  const sel = 'h-8 rounded-lg border border-line bg-surface px-2 text-[12.5px] text-muted outline-none focus:border-accent'
  return (
    <Card>
      <CardHeader
        title="执行历史"
        hint="定时与手动执行都会记录，保留 30 天"
        actions={
          <div className="flex flex-wrap gap-1.5">
            <select className={sel} value={task} onChange={(e) => setTask(e.target.value)} aria-label="任务">
              <option value="">全部任务</option>
              {tasks.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </select>
            <select className={sel} value={status} onChange={(e) => setStatus(e.target.value)} aria-label="结果">
              {statusOpts.map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </div>
        }
      />
      {isLoading ? (
        <div className="px-5 pb-5">
          <Skeleton className="h-24" />
        </div>
      ) : !runs?.length ? (
        <Empty icon={<History size={26} />} title="暂无执行记录" hint="任务到点执行或手动执行后，这里会显示每个账号的结果。" />
      ) : (
        <ul className="divide-y divide-line border-t border-line">
          {runs.map((r) => (
            <li key={r.id}>
              <button onClick={() => onOpen(r.id)} className="grid w-full grid-cols-[1fr_auto] items-center gap-x-4 gap-y-1 px-5 py-3 text-left hover:bg-sunken/40 sm:grid-cols-[150px_1fr_auto_auto]">
                <span className="num text-[12px] text-muted">{fmtDate(r.started_at)}</span>
                <span className="order-first flex min-w-0 items-center gap-2 sm:order-none">
                  <span className="truncate text-[13.5px] font-medium">{r.task_name}</span>
                  <span className="shrink-0 text-[11.5px] text-faint">
                    {triggerLabel(r.trigger)}
                    {r.scope ? ` · ${r.scope} 个账号` : ''}
                  </span>
                </span>
                <span className="hidden min-w-0 truncate text-[12.5px] text-muted sm:block">
                  {runLine(r)}
                  {(r.summary.credit > 0 || r.summary.energy > 0) && <span className="ml-2 font-medium text-ok">{rewardText(r.summary.credit, r.summary.energy)}</span>}
                </span>
                <span className="row-span-2 flex items-center gap-3 justify-self-end sm:row-span-1">
                  <span className="num hidden text-[11.5px] text-faint md:inline">{duration(r)}</span>
                  <RunStatusBadge status={r.status} />
                </span>
                <span className="truncate text-[12px] text-muted sm:hidden">{runLine(r)}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}

export function TasksPage() {
  const details = useProviderDetails().filter((d) => d.tasks?.length)
  const { data: latest } = useLatestRuns()
  const [open, setOpen] = useState<string | null>(null)

  if (!details.length)
    return (
      <Card>
        <Empty icon={<CalendarClock size={28} />} title="没有可用任务" hint="已启用的平台没有声明定时任务。" />
      </Card>
    )

  const allTasks = details.flatMap((d) => d.tasks!.map((t) => ({ id: t.id, name: details.length > 1 ? `${d.meta.name} · ${t.name}` : t.name })))

  return (
    <div className="space-y-6">
      {details.map((d) => (
        <Card key={d.meta.id}>
          <CardHeader
            title={d.meta.name}
            hint="立即执行作用于全部账号；只对部分账号执行，可在账号页勾选后从底部操作栏选择任务"
            actions={
              d.meta.capabilities?.includes('settings') && (
                <Link to={`/settings?tab=${d.meta.id}`} className="inline-flex items-center gap-1 whitespace-nowrap text-[12.5px] font-medium text-muted hover:text-accent">
                  调整执行时间 <ArrowUpRight size={13} />
                </Link>
              )
            }
          />
          <ul className="grid gap-px overflow-hidden rounded-b-2xl border-t border-line bg-line sm:grid-cols-2 xl:grid-cols-3">
            {d.tasks!.map((t) => (
              <TaskCard key={t.id} pid={d.meta.id} t={t} last={latest?.[`${d.meta.id}/${t.id}`]} onOpen={setOpen} />
            ))}
          </ul>
        </Card>
      ))}
      <RunHistory tasks={allTasks} onOpen={setOpen} />
      <RunDrawer id={open} onClose={() => setOpen(null)} />
    </div>
  )
}
