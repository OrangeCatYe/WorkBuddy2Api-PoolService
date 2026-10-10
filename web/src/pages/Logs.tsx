import { useSearchParams } from 'react-router-dom'
import { useMemo, useState } from 'react'
import { Pause, Play, ScrollText, Search } from 'lucide-react'
import { useKeys, useMeta, useRequests } from '@/lib/queries'
import { fmtNum, fmtTokens } from '@/lib/format'
import { Drawer } from '@/components/overlay'
import { CopyButton } from '@/components/copy'
import { Badge, Button, Card, Empty, Skeleton, cx } from '@/components/ui'
import type { Protocol, RequestEntry } from '@/types'

const protoLabel: Record<Protocol, string> = { chat: 'Chat', messages: 'Messages', responses: 'Responses' }
const protoTone: Record<Protocol, 'neutral' | 'info' | 'accent'> = { chat: 'neutral', messages: 'accent', responses: 'info' }

const clock = (iso: string) => {
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}
const ms = (n: number) => (n >= 10_000 ? `${(n / 1000).toFixed(0)}s` : n >= 1000 ? `${(n / 1000).toFixed(1)}s` : `${n}ms`)

function Field({ k, v, mono }: { k: string; v?: React.ReactNode; mono?: boolean }) {
  if (v === undefined || v === '' || v === null) return null
  return (
    <div className="flex items-baseline justify-between gap-4 py-1.5 text-[13px]">
      <span className="shrink-0 text-muted">{k}</span>
      <span className={cx('min-w-0 break-all text-right', mono && 'num')}>{v}</span>
    </div>
  )
}

function Detail({ e, onClose }: { e: RequestEntry; onClose: () => void }) {
  const cacheRate = e.prompt_tokens ? Math.round((e.cached_tokens / e.prompt_tokens) * 100) : 0
  return (
    <Drawer
      open
      onClose={onClose}
      title={e.requested_model || e.model || '请求'}
      subtitle={
        <span className="num inline-flex items-center gap-1">
          {e.id}
          <CopyButton text={e.id} />
        </span>
      }
    >
      <div className="flex flex-wrap gap-1.5">
        <Badge tone={e.ok ? 'ok' : 'bad'}>{e.status}</Badge>
        <Badge tone={protoTone[e.protocol]}>{protoLabel[e.protocol]}</Badge>
        {e.stream && <Badge>流式</Badge>}
        {e.effort && <Badge tone="accent">思考 {e.effort_requested && e.effort_requested !== e.effort ? `${e.effort_requested} → ${e.effort}` : e.effort}</Badge>}
      </div>
      {e.error && <div className="mt-3 rounded-xl border border-bad/30 bg-bad/[.06] px-3.5 py-2.5 text-[12.5px] text-bad">{e.error}</div>}

      <div className="mt-5 grid grid-cols-3 gap-2">
        {[
          ['输入', fmtTokens(e.prompt_tokens)],
          ['输出', fmtTokens(e.completion_tokens)],
          ['积分', e.has_credit ? fmtNum(e.credit) : '—'],
        ].map(([k, v]) => (
          <div key={k} className="rounded-xl bg-sunken/70 px-3 py-2.5">
            <div className="eyebrow">{k}</div>
            <div className="num mt-0.5 font-display text-[18px] font-semibold">{v}</div>
          </div>
        ))}
      </div>

      <section className="mt-5">
        <div className="eyebrow mb-1">路由</div>
        <Field k="请求模型" v={e.requested_model} mono />
        <Field k="实际模型" v={e.provider && e.model ? `${e.provider}/${e.model}` : undefined} mono />
        <Field k="账号" v={e.account_id} mono />
        <Field k="尝试次数" v={e.attempts && e.attempts > 1 ? `${e.attempts} 次（发生换号）` : e.attempts} />
        <Field k="密钥" v={e.key_name} />
      </section>
      <section className="mt-4">
        <div className="eyebrow mb-1">用量与耗时</div>
        <Field k="缓存命中" v={e.cached_tokens ? `${fmtTokens(e.cached_tokens)}（${cacheRate}%）` : '0'} />
        <Field k="推理 token" v={e.reasoning_tokens ? fmtTokens(e.reasoning_tokens) : undefined} />
        <Field k="首字耗时" v={e.ttfb_ms ? ms(e.ttfb_ms) : undefined} />
        <Field k="总耗时" v={ms(e.duration_ms)} />
      </section>
      <section className="mt-4">
        <div className="eyebrow mb-1">来源</div>
        <Field k="时间" v={new Date(e.time).toLocaleString('zh-CN')} />
        <Field k="客户端 IP" v={e.client_ip} mono />
        <Field k="User-Agent" v={e.user_agent} mono />
      </section>
    </Drawer>
  )
}

export function LogsPage() {
  const { data: meta } = useMeta()
  const { data: keys } = useKeys()
  const [live, setLive] = useState(true)
  const [search] = useSearchParams()
  const [f, setF] = useState(() => ({ protocol: search.get('protocol') ?? '', status: search.get('status') ?? '', provider: search.get('provider') ?? '', key: search.get('key') ?? '', q: search.get('q') ?? '' }))
  const [open, setOpen] = useState<RequestEntry | null>(null)
  const params = useMemo(() => Object.fromEntries(Object.entries({ ...f, limit: '300' }).filter(([, v]) => v)), [f])
  const { data: rows, isLoading } = useRequests(params, live)

  const sel = 'h-9 rounded-lg border border-line bg-surface px-2.5 text-[13px] text-muted outline-none focus:border-accent'
  const summary = useMemo(() => {
    const r = rows ?? []
    return { n: r.length, fail: r.filter((x) => !x.ok).length, credit: r.reduce((s, x) => s + (x.credit || 0), 0) }
  }, [rows])

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <select className={sel} value={f.protocol} onChange={(e) => setF({ ...f, protocol: e.target.value })}>
          <option value="">全部协议</option>
          <option value="chat">Chat Completions</option>
          <option value="messages">Anthropic Messages</option>
          <option value="responses">Responses</option>
        </select>
        <select className={sel} value={f.status} onChange={(e) => setF({ ...f, status: e.target.value })}>
          <option value="">全部结果</option>
          <option value="ok">成功</option>
          <option value="error">失败</option>
        </select>
        {(meta?.providers.length ?? 0) > 1 && (
          <select className={sel} value={f.provider} onChange={(e) => setF({ ...f, provider: e.target.value })}>
            <option value="">全部平台</option>
            {meta!.providers.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        )}
        <select className={sel} value={f.key} onChange={(e) => setF({ ...f, key: e.target.value })}>
          <option value="">全部密钥</option>
          {keys?.map((k) => (
            <option key={k.id} value={k.id}>
              {k.name}
            </option>
          ))}
        </select>
        <label className="flex h-9 w-full items-center gap-2 rounded-lg border border-line bg-surface px-3 focus-within:border-accent sm:w-56">
          <Search size={14} className="text-faint" />
          <input value={f.q} onChange={(e) => setF({ ...f, q: e.target.value })} placeholder="模型、账号、错误、ID" className="w-full bg-transparent text-[13px] outline-none" />
        </label>
        <div className="flex w-full items-center justify-between gap-3 text-[12.5px] text-muted sm:ml-auto sm:w-auto sm:justify-end">
          <span className="num">
            {summary.n} 条 · 失败 <span className={summary.fail ? 'text-bad' : ''}>{summary.fail}</span> · 积分 {fmtNum(summary.credit)}
          </span>
          <Button size="sm" onClick={() => setLive(!live)} icon={live ? <Pause size={12} /> : <Play size={12} />}>
            {live ? '实时' : '已暂停'}
          </Button>
        </div>
      </div>

      <Card className="overflow-hidden">
        <div className="hidden overflow-x-auto md:block">
        <table className="w-full min-w-[880px] whitespace-nowrap text-left">
          <thead>
            <tr className="border-b border-line text-[12px] text-faint">
              <th className="py-3 pl-5 font-medium">时间</th>
              <th className="px-3 py-3 font-medium">协议</th>
              <th className="px-3 py-3 font-medium">模型</th>
              <th className="px-3 py-3 font-medium">账号 / 密钥</th>
              <th className="px-3 py-3 font-medium">状态</th>
              <th className="px-3 py-3 text-right font-medium">输入 / 输出</th>
              <th className="px-3 py-3 text-right font-medium">积分</th>
              <th className="px-5 py-3 text-right font-medium">首字 / 总耗时</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {isLoading &&
              [0, 1, 2, 3].map((i) => (
                <tr key={i}>
                  <td colSpan={8} className="px-5 py-2.5">
                    <Skeleton className="h-5" />
                  </td>
                </tr>
              ))}
            {rows?.map((e) => (
              <tr key={e.id} onClick={() => setOpen(e)} className="cursor-pointer text-[13px] hover:bg-sunken/40">
                <td className="num whitespace-nowrap py-2.5 pl-5 text-[12px] text-muted">{clock(e.time)}</td>
                <td className="px-3 py-2.5">
                  <Badge tone={protoTone[e.protocol]}>{protoLabel[e.protocol]}</Badge>
                </td>
                <td className="max-w-[220px] px-3 py-2.5">
                  <div className="num truncate">{e.model || e.requested_model}</div>
                  <div className="text-[11.5px] text-faint">
                    {e.stream ? '流式' : '非流式'}
                    {e.effort ? ` · ${e.effort}` : ''}
                  </div>
                </td>
                <td className="max-w-[160px] px-3 py-2.5">
                  <div className="num truncate text-[12.5px]">{e.account_id ? e.account_id.slice(0, 8) : '—'}</div>
                  <div className="truncate text-[11.5px] text-faint">{e.key_name}</div>
                </td>
                <td className="px-3 py-2.5">
                  <Badge tone={e.ok ? 'ok' : e.status === 429 ? 'warn' : 'bad'}>{e.status}</Badge>
                  {e.error && <div className="mt-0.5 max-w-[180px] truncate text-[11.5px] text-bad">{e.error}</div>}
                </td>
                <td className="num px-3 py-2.5 text-right text-[12.5px]">
                  {fmtTokens(e.prompt_tokens)} / {fmtTokens(e.completion_tokens)}
                  {e.cached_tokens ? <div className="text-[11px] text-ok">缓存 {fmtTokens(e.cached_tokens)}</div> : null}
                </td>
                <td className="num px-3 py-2.5 text-right">{e.has_credit ? fmtNum(e.credit) : '—'}</td>
                <td className="num px-5 py-2.5 text-right text-[12.5px] text-muted">
                  {e.ttfb_ms ? ms(e.ttfb_ms) : '—'} / {ms(e.duration_ms)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        </div>
        <ul className="divide-y divide-line md:hidden">
          {isLoading && (
            <li className="p-4">
              <Skeleton className="h-12" />
            </li>
          )}
          {rows?.map((e) => (
            <li key={e.id}>
              <button onClick={() => setOpen(e)} className="flex w-full items-start gap-3 px-4 py-3 text-left active:bg-sunken/50">
                <span className={cx('mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full', e.ok ? 'bg-ok' : 'bg-bad')} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline justify-between gap-2">
                    <span className="num truncate text-[13.5px] font-medium">{e.model || e.requested_model || '—'}</span>
                    <span className="num shrink-0 text-[12.5px]">{e.has_credit ? fmtNum(e.credit) : '—'}</span>
                  </div>
                  <div className="mt-0.5 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[11.5px] text-faint">
                    <span className="num">{clock(e.time)}</span>
                    <span>·</span>
                    <span>{protoLabel[e.protocol]}</span>
                    <span>·</span>
                    <span className="num">
                      {fmtTokens(e.prompt_tokens)} → {fmtTokens(e.completion_tokens)}
                    </span>
                    {e.ttfb_ms ? <span className="num">· 首字 {ms(e.ttfb_ms)}</span> : null}
                  </div>
                  {!e.ok && (
                    <div className="mt-1 truncate text-[11.5px] text-bad">
                      {e.status} {e.error_kind || e.error}
                    </div>
                  )}
                </div>
              </button>
            </li>
          ))}
        </ul>
        {!isLoading && !rows?.length && <Empty icon={<ScrollText size={28} />} title="暂无请求" hint="网关收到的请求会实时出现在这里（只记录元数据，不保存对话内容）。" />}
      </Card>
      {open && <Detail e={open} onClose={() => setOpen(null)} />}
    </div>
  )
}
