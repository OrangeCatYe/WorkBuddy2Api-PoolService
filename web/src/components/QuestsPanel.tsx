import { useEffect, useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ChevronDown, Gift, ListChecks, Loader2, Lock, Sparkles, Wand2 } from 'lucide-react'
import { post } from '@/lib/api'
import { useQuests, useTaskRun } from '@/lib/queries'
import { fmtNum } from '@/lib/format'
import type { Account, Quest } from '@/types'
import { useToast } from './overlay'
import { Badge, Button, Empty, Meter, Skeleton, cx } from './ui'
import { RunItems, RunStatusBadge, SummaryBar, rewardText } from './TaskRun'

function Reward({ q }: { q: Quest }) {
  const t = rewardText(q.credit, q.energy)
  return t ? <span className="num whitespace-nowrap text-[11.5px] font-medium text-accent">{t}</span> : null
}

function QuestRow({ q, busy, onRun, onClaim }: { q: Quest; busy: boolean; onRun: () => void; onClaim: () => void }) {
  const ratio = q.target > 0 ? Math.min(100, (q.current / q.target) * 100) : 0
  return (
    <li className={cx('px-3.5 py-3', (q.status === 'claimed' || q.status === 'locked') && 'opacity-60')}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-[13px] font-medium">{q.title}</span>
            {q.tag && <Badge className="!px-1 !py-0 text-[10.5px]">{q.tag}</Badge>}
            {q.attempt && <Badge tone="warn" className="!px-1 !py-0 text-[10.5px]">尝试</Badge>}
          </div>
          {q.description && <p className="mt-0.5 line-clamp-2 text-[11.5px] text-faint">{q.description}</p>}
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1.5">
          <Reward q={q} />
          {q.status === 'claimable' ? (
            <Button size="sm" variant="primary" disabled={busy} onClick={onClaim} icon={<Gift size={12} />}>
              领取
            </Button>
          ) : q.status === 'todo' && q.auto ? (
            <Button size="sm" disabled={busy} onClick={onRun} icon={<Wand2 size={12} />} title={q.auto_desc}>
              自动完成
            </Button>
          ) : null}
        </div>
      </div>
      {q.status === 'todo' && (
        <div className="mt-2 flex items-center gap-2">
          {q.target > 0 && (
            <>
              <Meter value={ratio} className="flex-1" />
              <span className="num shrink-0 text-[11px] text-muted">
                {q.current}/{q.target}
              </span>
            </>
          )}
          {!q.auto && <span className="shrink-0 text-[11px] text-faint">需在客户端完成</span>}
        </div>
      )}
      {q.status === 'todo' && q.auto && q.auto_desc && <p className="mt-1 text-[11px] text-faint">自动：{q.auto_desc}</p>}
    </li>
  )
}

function Group({ title, icon, quests, collapsed, ...rest }: { title: string; icon: React.ReactNode; quests: Quest[]; collapsed?: boolean; busy: boolean; onRun: (q: Quest) => void; onClaim: (q: Quest) => void }) {
  const [open, setOpen] = useState(!collapsed)
  if (!quests.length) return null
  return (
    <section>
      <button onClick={() => setOpen(!open)} className="mb-1.5 flex w-full items-center justify-between text-left">
        <span className="eyebrow flex items-center gap-1.5">
          {icon}
          {title} · {quests.length}
        </span>
        <ChevronDown size={14} className={cx('text-faint transition-transform', !open && '-rotate-90')} />
      </button>
      {open && (
        <ul className="divide-y divide-line rounded-xl border border-line">
          {quests.map((q) => (
            <QuestRow key={q.code} q={q} busy={rest.busy} onRun={() => rest.onRun(q)} onClaim={() => rest.onClaim(q)} />
          ))}
        </ul>
      )}
    </section>
  )
}

export function QuestsPanel({ account }: { account: Account }) {
  const pid = account.provider
  const aid = account.id
  const { data: quests, isLoading, error, refetch, isFetching } = useQuests(pid, aid)
  const [runId, setRunId] = useState<string | null>(null)
  const { data: run } = useTaskRun(runId)
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const qc = useQueryClient()
  const running = !!runId && !run?.ended_at

  // 执行结束：刷新任务列表与账号余额
  useEffect(() => {
    if (run?.ended_at) {
      qc.invalidateQueries({ queryKey: ['quests', pid, aid] })
      qc.invalidateQueries({ queryKey: ['accounts'] })
    }
  }, [run?.ended_at, pid, aid, qc])

  const groups = useMemo(() => {
    const g = { claimable: [] as Quest[], todo: [] as Quest[], manual: [] as Quest[], locked: [] as Quest[], claimed: [] as Quest[] }
    quests?.forEach((q) => {
      if (q.status === 'todo' && !q.auto) g.manual.push(q)
      else g[q.status].push(q)
    })
    return g
  }, [quests])

  const base = `/api/providers/${encodeURIComponent(pid)}/accounts/${encodeURIComponent(aid)}/quests`
  async function start(codes?: string[]) {
    setBusy(true)
    try {
      const r = await post<{ run_id: string }>(`${base}/run`, codes ? { codes } : {})
      setRunId(r.run_id)
      qc.invalidateQueries({ queryKey: ['task-runs'] })
    } catch (e) {
      toast.bad((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  async function claim(q: Quest) {
    setBusy(true)
    try {
      const r = await post<{ credit: number; energy: number; already: boolean }>(`${base}/${encodeURIComponent(q.code)}/claim`)
      toast.ok(r.already ? `${q.title}：奖励此前已领取` : `${q.title}：${rewardText(r.credit, r.energy) || '已领取'}`)
      refetch()
      qc.invalidateQueries({ queryKey: ['accounts'] })
    } catch (e) {
      toast.bad((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  async function acceptAll() {
    setBusy(true)
    try {
      const r = await post<{ accepted: number; message?: string }>(`${base}/accept`)
      toast.ok(r.accepted ? `已接受 ${r.accepted} 个任务${r.message ? `（${r.message}）` : ''}` : '所有任务均已接受')
      refetch()
    } catch (e) {
      toast.bad((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (isLoading) return <Skeleton className="h-48" />
  if (error) return <Empty icon={<ListChecks size={24} />} title="无法读取任务" hint={(error as Error).message} />
  if (!quests?.length) return <Empty icon={<ListChecks size={24} />} title="该账号没有任务" />

  const reward = (list: Quest[]) => list.reduce((s, q) => s + (q.credit ?? 0), 0)
  const autoTodo = groups.todo.length + groups.claimable.length
  const pendingReward = reward(groups.todo) + reward(groups.claimable)

  return (
    <div className="space-y-5">
      <div className="rounded-xl bg-sunken/60 p-3.5">
        <div className="grid grid-cols-3 gap-2 text-center">
          {[
            ['可自动', groups.todo.length, ''],
            ['待领奖', groups.claimable.length, groups.claimable.length ? 'text-accent' : ''],
            ['已完成', groups.claimed.length, 'text-ok'],
          ].map(([k, v, c]) => (
            <div key={k as string}>
              <div className={cx('num font-display text-[22px] font-semibold leading-none', c as string)}>{v}</div>
              <div className="mt-1 text-[11.5px] text-muted">{k}</div>
            </div>
          ))}
        </div>
        {pendingReward > 0 && <p className="mt-2.5 text-center text-[12px] text-muted">待完成任务共可获得约 <b className="num text-accent">{fmtNum(pendingReward)}</b> 积分</p>}
        <div className="mt-3 flex gap-2">
          <Button variant="primary" className="flex-1" disabled={busy || running || !autoTodo} onClick={() => start()} icon={running ? <Loader2 size={14} className="animate-spin" /> : <Sparkles size={14} />}>
            {running ? '执行中…' : autoTodo ? `自动完成全部 ${autoTodo} 项` : '没有可自动完成的任务'}
          </Button>
          <Button disabled={busy || running} onClick={acceptAll}>
            全部接受
          </Button>
        </div>
        <p className="mt-2 text-[11px] leading-relaxed text-faint">部分任务包含真实对话或需要按真人节奏间隔上报，单个账号可能需要数分钟。可以关掉面板，结果会记录在任务页的执行历史里。</p>
      </div>

      {run && (
        <section className="rounded-xl border border-line p-3.5">
          <div className="mb-2 flex items-center justify-between">
            <span className="text-[13px] font-medium">本次执行</span>
            <RunStatusBadge status={run.status} />
          </div>
          <SummaryBar s={run.summary} className="mb-3" />
          {run.note && <p className="mb-2 text-[12px] text-muted">{run.note}</p>}
          <RunItems items={run.items ?? []} showAccount={false} />
        </section>
      )}

      <Group title="待领奖" icon={<Gift size={12} />} quests={groups.claimable} busy={busy || running} onRun={(q) => start([q.code])} onClaim={claim} />
      <Group title="可自动完成" icon={<Wand2 size={12} />} quests={groups.todo} busy={busy || running} onRun={(q) => start([q.code])} onClaim={claim} />
      <Group title="需在客户端完成" icon={<ListChecks size={12} />} quests={groups.manual} collapsed busy={busy} onRun={() => {}} onClaim={claim} />
      <Group title="未解锁" icon={<Lock size={12} />} quests={groups.locked} collapsed busy={busy} onRun={() => {}} onClaim={claim} />
      <Group title="已完成" icon={<ListChecks size={12} />} quests={groups.claimed} collapsed busy={busy} onRun={() => {}} onClaim={claim} />
      <button onClick={() => refetch()} disabled={isFetching} className="w-full text-center text-[12px] text-faint hover:text-ink disabled:opacity-50">
        {isFetching ? '刷新中…' : '刷新任务列表'}
      </button>
    </div>
  )
}
