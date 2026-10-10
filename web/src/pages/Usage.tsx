import { useState } from 'react'
import { Activity } from 'lucide-react'
import { useAccounts, useUsageStats } from '@/lib/queries'
import { fmtNum, fmtTokens, pct } from '@/lib/format'
import { Segmented } from '@/components/overlay'
import { Card, CardHeader, Empty, Meter, Skeleton, cx } from '@/components/ui'
import type { UsageBucket } from '@/types'

type Metric = 'requests' | 'credit' | 'tokens'

const metricOf = (b: UsageBucket, m: Metric) => (m === 'requests' ? b.requests : m === 'credit' ? b.credit : b.prompt_tokens + b.completion_tokens)
const fmtMetric = (v: number, m: Metric) => (m === 'tokens' ? fmtTokens(v) : fmtNum(v))

function Kpi({ label, value, sub, tone }: { label: string; value: string; sub?: string; tone?: 'ok' | 'bad' }) {
  return (
    <Card className="px-5 py-4">
      <div className="eyebrow">{label}</div>
      <div className={cx('num mt-1 font-display text-[26px] font-semibold leading-none', tone === 'bad' && 'text-bad')}>{value}</div>
      {sub && <div className="mt-1.5 text-[12px] text-muted">{sub}</div>}
    </Card>
  )
}

function Series({ data, metric, hours }: { data: UsageBucket[]; metric: Metric; hours: number }) {
  const [hover, setHover] = useState<number | null>(null)
  const vals = data.map((b) => metricOf(b, metric))
  const max = Math.max(1, ...vals)
  const W = 1000
  const H = 180
  const gap = data.length > 60 ? 1 : 3
  const bw = W / Math.max(1, data.length) - gap
  const labelEvery = hours <= 24 ? 3 : hours <= 72 ? 12 : 24
  const h = hover !== null ? data[hover] : null

  return (
    <div className="px-5 pb-4">
      <div className="h-5 text-right text-[12px] text-muted">
        {h ? (
          <span className="num">
            {new Date(h.key).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })} · {h.requests} 次 · 积分 {fmtNum(h.credit)} · {fmtTokens(h.prompt_tokens + h.completion_tokens)} token
            {h.failures ? <span className="text-bad"> · 失败 {h.failures}</span> : null}
          </span>
        ) : (
          <span className="num">峰值 {fmtMetric(max, metric)}</span>
        )}
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} className="mt-2 h-[180px] w-full" preserveAspectRatio="none" onMouseLeave={() => setHover(null)}>
        {[0.25, 0.5, 0.75].map((r) => (
          <line key={r} x1={0} x2={W} y1={H * r} y2={H * r} className="stroke-line" strokeDasharray="3 5" vectorEffect="non-scaling-stroke" />
        ))}
        <line x1={0} x2={W} y1={H} y2={H} className="stroke-line" vectorEffect="non-scaling-stroke" />
        {data.map((b, i) => {
          const v = vals[i]
          const bh = v ? Math.max(2, (v / max) * (H - 8)) : 0
          const x = i * (bw + gap)
          const failH = metric === 'requests' && b.failures ? (b.failures / max) * (H - 8) : 0
          return (
            <g key={b.key} onMouseEnter={() => setHover(i)}>
              <rect x={x} y={0} width={bw + gap} height={H} fill="transparent" />
              <rect x={x} y={H - bh} width={bw} height={bh} rx={Math.min(3, bw / 2)} className={cx(hover === i ? 'fill-accent' : 'fill-accent/70', 'transition-colors')} />
              {failH > 0 && <rect x={x} y={H - failH} width={bw} height={failH} rx={Math.min(3, bw / 2)} className="fill-bad" />}
            </g>
          )
        })}
      </svg>
      <div className="relative mt-1.5 h-4">
        {data.map((b, i) =>
          i % labelEvery === 0 ? (
            <span key={b.key} className="num absolute -translate-x-1/2 text-[11px] text-faint" style={{ left: `${((i + 0.5) / data.length) * 100}%` }}>
              {hours <= 24 ? `${new Date(b.key).getHours()}时` : `${new Date(b.key).getMonth() + 1}/${new Date(b.key).getDate()}${hours <= 72 ? ` ${new Date(b.key).getHours()}时` : ''}`}
            </span>
          ) : null,
        )}
      </div>
    </div>
  )
}

function Breakdown({ title, rows, total, empty, label }: { title: string; rows: UsageBucket[]; total: UsageBucket; empty: string; label?: (k: string) => string }) {
  // 积分全为 0（免费模型 / 无积分平台）时按请求数占比
  const byCredit = total.credit > 0
  const val = (r: UsageBucket) => (byCredit ? r.credit : r.requests)
  const max = Math.max(1, ...rows.map(val))
  const sum = byCredit ? total.credit : total.requests
  return (
    <Card>
      <CardHeader title={title} />
      {rows.length ? (
        <ul className="divide-y divide-line border-t border-line">
          {rows.slice(0, 10).map((r) => (
            <li key={r.key} className="px-5 py-2.5">
              <div className="flex items-baseline justify-between gap-3 text-[13px]">
                <span className="num min-w-0 truncate" title={r.key}>{r.key === '-' ? '未路由（请求被拒绝）' : (label?.(r.key) ?? r.key)}</span>
                <span className="num shrink-0 text-[12.5px]">
                  <span className="font-semibold">{fmtNum(r.credit)}</span>
                  <span className="text-faint"> 积分 · {r.requests} 次</span>
                  {r.failures ? <span className="text-bad"> · 失败 {r.failures}</span> : null}
                </span>
              </div>
              <div className="mt-1.5 flex items-center gap-3">
                <Meter value={pct(val(r), max)} className="flex-1" />
                <span className="num w-[110px] shrink-0 text-right text-[11px] text-faint">
                  {fmtTokens(r.prompt_tokens + r.completion_tokens) || 0} · {sum ? Math.round((val(r) / sum) * 100) : 0}%
                </span>
              </div>
            </li>
          ))}
        </ul>
      ) : (
        <p className="border-t border-line px-5 py-6 text-center text-[12.5px] text-faint">{empty}</p>
      )}
    </Card>
  )
}

export function UsagePage() {
  const [hours, setHours] = useState<'24' | '72' | '168'>('24')
  const [metric, setMetric] = useState<Metric>('requests')
  const { data, isLoading } = useUsageStats(Number(hours))
  const { data: accounts } = useAccounts()
  const acctLabel = (k: string) => {
    const a = accounts?.find((x) => `${x.provider}/${x.id}` === k)
    const [p, id] = k.split('/')
    return a ? `${a.label} · ${p}/${a.id.slice(0, 8)}` : `${p}/${(id ?? '').slice(0, 8)}`
  }

  if (isLoading || !data) return <Skeleton className="h-72" />
  const t = data.total
  const success = t.requests ? ((t.requests - t.failures) / t.requests) * 100 : 0
  const cache = t.prompt_tokens ? (t.cached_tokens / t.prompt_tokens) * 100 : 0

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <Segmented
          value={hours}
          onChange={setHours}
          options={[
            { value: '24', label: '24 小时' },
            { value: '72', label: '3 天' },
            { value: '168', label: '7 天' },
          ]}
        />
        <span className="text-[12px] text-faint">统计基于最近 5000 条请求记录</span>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Kpi label="请求" value={fmtNum(t.requests)} sub={t.requests ? `成功率 ${success.toFixed(1)}%` : '暂无请求'} tone={t.requests && success < 90 ? 'bad' : undefined} />
        <Kpi label="消耗积分" value={fmtNum(t.credit)} sub={t.requests ? `平均每次 ${(t.credit / t.requests).toFixed(2)}` : undefined} />
        <Kpi label="Token" value={fmtTokens(t.prompt_tokens + t.completion_tokens) || '0'} sub={`输入 ${fmtTokens(t.prompt_tokens) || 0} · 输出 ${fmtTokens(t.completion_tokens) || 0}`} />
        <Kpi label="缓存命中" value={`${cache.toFixed(1)}%`} sub={t.avg_ttfb_ms ? `平均首字 ${(t.avg_ttfb_ms / 1000).toFixed(2)}s` : undefined} />
      </div>

      <Card>
        <CardHeader
          title="趋势"
          hint="按小时汇总；红色为失败请求"
          actions={
            <Segmented
              value={metric}
              onChange={setMetric}
              options={[
                { value: 'requests', label: '请求' },
                { value: 'credit', label: '积分' },
                { value: 'tokens', label: 'Token' },
              ]}
            />
          }
        />
        {t.requests ? <Series data={data.series} metric={metric} hours={Number(hours)} /> : <Empty icon={<Activity size={26} />} title="这段时间没有请求" />}
      </Card>

      <div className="grid gap-6 lg:grid-cols-2">
        <Breakdown title="按模型" rows={data.by_model} total={t} empty="暂无数据" />
        <Breakdown title="按账号" rows={data.by_account} total={t} empty="暂无数据" label={acctLabel} />
        <Breakdown title="按密钥" rows={data.by_key} total={t} empty="暂无数据" />
        <Breakdown title="按协议" rows={data.by_protocol} total={t} empty="暂无数据" label={(k) => ({ chat: 'Chat Completions', messages: 'Anthropic Messages', responses: 'OpenAI Responses' })[k] ?? k} />
      </div>
    </div>
  )
}
