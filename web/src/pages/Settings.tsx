import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { AlertTriangle, ArrowUpRight, RotateCcw, RotateCw, Search } from 'lucide-react'
import { api } from '@/lib/api'
import { useGatewaySettings, useMeta, useModels, useProviderSettings, useUpdateStatus } from '@/lib/queries'
import { Segmented, Switch, useToast } from '@/components/overlay'
import { UpdatePanel } from '@/components/UpdatePanel'
import { formatCredit } from '@/lib/format'
import { setCreditPrecision, useCreditPrecision } from '@/lib/creditDisplay'
import { Badge, Button, Card, CardHeader, Empty, Skeleton, cx } from '@/components/ui'
import type { GatewaySettings, ProviderSettings, SettingField } from '@/types'

function SettingRow({ title, hint, control, badge, disabled, stacked }: { title: string; hint?: React.ReactNode; control: React.ReactNode; badge?: React.ReactNode; disabled?: boolean; stacked?: boolean }) {
  return (
    <div className={cx('flex gap-x-6 gap-y-2 px-5 py-3.5 transition-opacity', stacked ? 'flex-col' : 'flex-col sm:flex-row sm:items-start sm:justify-between', disabled && 'opacity-45')}>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-1.5 text-[13.5px] font-medium">
          {title}
          {badge}
        </div>
        {hint && <div className="mt-0.5 text-[12.5px] text-muted">{hint}</div>}
      </div>
      <div className={cx('shrink-0', !stacked && 'sm:pt-0.5')}>{control}</div>
    </div>
  )
}

// ---------------- 显示偏好 ----------------

function CreditPrecisionToggle() {
  const p = useCreditPrecision()
  const two = p === 'two'
  const samples = [12.34, 8, 12580]
  return (
    <div className="flex flex-col items-end gap-1.5">
      <Switch checked={two} onChange={(v) => setCreditPrecision(v ? 'two' : 'int')} />
      <div className="num text-[11.5px] text-faint">
        {samples.map((s) => formatCredit(s, p)).join(' · ')}
      </div>
    </div>
  )
}

// ---------------- 网关 ----------------

function GatewayTab() {
  const { data, isLoading } = useGatewaySettings()
  const { data: models } = useModels()
  const qc = useQueryClient()
  const toast = useToast()
  const m = useMutation({
    mutationFn: (s: GatewaySettings) => api<GatewaySettings>('/api/settings/gateway', { method: 'PUT', body: JSON.stringify(s) }),
    onMutate: (s) => qc.setQueryData(['settings', 'gateway'], s),
    onSuccess: (s) => {
      qc.setQueryData(['settings', 'gateway'], s)
      qc.invalidateQueries({ queryKey: ['meta'] })
      qc.invalidateQueries({ queryKey: ['models'] })
      toast.ok('已保存，立即生效')
    },
    onError: (e: Error) => {
      toast.bad(e.message)
      qc.invalidateQueries({ queryKey: ['settings', 'gateway'] })
    },
  })

  const sample = models?.models.find((x) => x.credits) ?? models?.models[0]
  const preview = (() => {
    if (!data || !sample) return null
    let id = data.model_naming === 'full' ? `${sample.provider}/${sample.id}` : sample.id
    if (data.credit_in_model_name && sample.credits) id += `(${sample.credits})`
    return id
  })()

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader title="模型命名" hint="影响 /v1/models 输出，Agent（如 ttswitch）重新拉取模型列表后生效" />
        {isLoading || !data ? (
          <div className="px-5 pb-5">
            <Skeleton className="h-16" />
          </div>
        ) : (
          <div className="divide-y divide-line border-t border-line">
            <SettingRow
              title="命名风格"
              hint="短名最简洁；多平台同名模型时，非首选平台会自动使用全名"
              control={
                <Segmented
                  value={data.model_naming}
                  onChange={(v) => m.mutate({ ...data, model_naming: v })}
                  options={[
                    { value: 'short', label: '短名' },
                    { value: 'full', label: '平台/模型' },
                    { value: 'both', label: '两者' },
                  ]}
                />
              }
            />
            <SettingRow
              title="模型名显示积分倍率"
              hint="在模型 ID 与显示名末尾附加倍率，Agent 的模型选择器里直接可见。带不带后缀都能请求，倍率随优惠变化后旧名仍可用。"
              control={<Switch checked={data.credit_in_model_name} onChange={(v) => m.mutate({ ...data, credit_in_model_name: v })} />}
            />
            {preview && (
              <div className="flex items-center gap-3 bg-sunken/50 px-5 py-3 text-[12.5px]">
                <span className="eyebrow">预览</span>
                <code className="num rounded-md bg-surface px-2 py-1 text-ink">{preview}</code>
              </div>
            )}
          </div>
        )}
      </Card>
      <Card>
        <CardHeader title="显示" hint="仅影响本浏览器界面，不改接口数据" />
        <div className="divide-y divide-line border-t border-line">
          <SettingRow
            title="积分显示精度"
            hint="默认显示到个位；开启两位小数可看清几分级别的消耗，与上游账单一致"
            control={<CreditPrecisionToggle />}
          />
        </div>
      </Card>
      <Card>
        <CardHeader title="其他设置" />
        <div className="divide-y divide-line border-t border-line">
          {[
            ['/credits', '积分策略', '到期优先、放大倍数、每日上限与余额保底'],
            ['/gateway', '下游密钥', '模型白名单、频率与积分预算'],
          ].map(([to, t, h]) => (
            <Link key={to} to={to} className="group flex items-center justify-between gap-4 px-5 py-3.5 hover:bg-sunken/40">
              <div>
                <div className="text-[13.5px] font-medium">{t}</div>
                <div className="mt-0.5 text-[12.5px] text-muted">{h}</div>
              </div>
              <ArrowUpRight size={15} className="text-faint group-hover:text-accent" />
            </Link>
          ))}
        </div>
      </Card>
    </div>
  )
}

// ---------------- 平台参数控件 ----------------

const fmtDuration = (s: string) => {
  const m = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/.exec(s.trim())
  if (!m || !s.trim()) return null
  const [h, mi, se] = [m[1], m[2], m[3]].map((x) => Number(x ?? 0))
  const total = h * 3600 + mi * 60 + se
  if (total === 0) return '0（关闭）'
  const parts: string[] = []
  const d = Math.floor(total / 86400)
  const hh = Math.floor((total % 86400) / 3600)
  const mm = Math.floor((total % 3600) / 60)
  const ss = total % 60
  if (d) parts.push(`${d} 天`)
  if (hh) parts.push(`${hh} 小时`)
  if (mm) parts.push(`${mm} 分钟`)
  if (ss) parts.push(`${ss} 秒`)
  return parts.join(' ')
}

const inputCls = 'h-8 rounded-lg border border-line bg-surface px-2.5 text-[13px] outline-none transition-colors focus:border-accent'

function NumberInput({ f, value, onChange, disabled }: { f: SettingField; value: unknown; onChange: (v: unknown) => void; disabled: boolean }) {
  const [text, setText] = useState(value === undefined || value === null ? '' : String(value))
  useEffect(() => setText(value === undefined || value === null ? '' : String(value)), [value])
  const n = Number(text)
  const invalid = text.trim() === '' || Number.isNaN(n) || (f.kind === 'int' && !Number.isInteger(n)) || (f.min !== undefined && n < f.min) || (f.max !== undefined && n > f.max)
  return (
    <div>
      <div className="flex items-center gap-1.5">
        <input
          type="number"
          inputMode={f.kind === 'int' ? 'numeric' : 'decimal'}
          step={f.kind === 'int' ? 1 : 'any'}
          min={f.min}
          max={f.max}
          disabled={disabled}
          value={text}
          onChange={(e) => {
            setText(e.target.value)
            const v = Number(e.target.value)
            if (e.target.value.trim() !== '' && !Number.isNaN(v)) onChange(v)
          }}
          className={cx(inputCls, 'num w-24 text-right', invalid && 'border-bad focus:border-bad')}
        />
        {f.unit && <span className="text-[12px] text-faint">{f.unit}</span>}
      </div>
      {invalid && f.min !== undefined && f.max !== undefined && <div className="mt-1 text-[11px] text-bad">范围 {f.min} – {f.max}</div>}
    </div>
  )
}

function DurationInput({ value, onChange, disabled }: { value: unknown; onChange: (v: unknown) => void; disabled: boolean }) {
  const s = typeof value === 'string' ? value : ''
  const human = fmtDuration(s)
  return (
    <div className="text-right">
      <input disabled={disabled} value={s} onChange={(e) => onChange(e.target.value)} placeholder="30m" spellCheck={false} className={cx(inputCls, 'num w-28 text-right', s && !human && 'border-bad focus:border-bad')} />
      <div className={cx('mt-1 text-[11px]', human ? 'text-faint' : 'text-bad')}>{human ?? '格式如 90s、30m、2h、1h30m'}</div>
    </div>
  )
}

function HoursInput({ value, onChange, disabled }: { value: unknown; onChange: (v: unknown) => void; disabled: boolean }) {
  const set = new Set(Array.isArray(value) ? (value as number[]) : [])
  return (
    <div className="grid grid-cols-12 gap-1">
      {Array.from({ length: 24 }, (_, h) => {
        const on = set.has(h)
        return (
          <button
            key={h}
            type="button"
            disabled={disabled}
            onClick={() => {
              const n = new Set(set)
              on ? n.delete(h) : n.add(h)
              if (n.size) onChange([...n].sort((a, b) => a - b))
            }}
            title={`${String(h).padStart(2, '0')}:00`}
            className={cx('num h-7 w-full min-w-[26px] rounded-md text-[11.5px] transition-colors', on ? 'bg-accent font-semibold text-accent-ink' : 'bg-sunken text-muted hover:text-ink')}
          >
            {h}
          </button>
        )
      })}
    </div>
  )
}

function FieldControl({ f, value, onChange, disabled, secretSet }: { f: SettingField; value: unknown; onChange: (v: unknown) => void; disabled: boolean; secretSet: boolean }) {
  switch (f.kind) {
    case 'bool':
      return <Switch checked={!!value} onChange={onChange} disabled={disabled} />
    case 'int':
    case 'float':
      return <NumberInput f={f} value={value} onChange={onChange} disabled={disabled} />
    case 'duration':
      return <DurationInput value={value} onChange={onChange} disabled={disabled} />
    case 'hours':
      return <HoursInput value={value} onChange={onChange} disabled={disabled} />
    case 'select':
      return f.options && f.options.length <= 4 ? (
        <Segmented value={String(value ?? '')} onChange={onChange} options={f.options} />
      ) : (
        <select disabled={disabled} value={String(value ?? '')} onChange={(e) => onChange(e.target.value)} className={inputCls}>
          {f.options?.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      )
    case 'secret':
      return <input type="password" autoComplete="new-password" disabled={disabled} value={String(value ?? '')} onChange={(e) => onChange(e.target.value)} placeholder={secretSet ? '已设置，留空不修改' : '未设置'} className={cx(inputCls, 'w-full sm:w-64')} />
    default:
      return <input disabled={disabled} value={String(value ?? '')} onChange={(e) => onChange(e.target.value)} spellCheck={false} className={cx(inputCls, 'w-full sm:w-64')} />
  }
}

// ---------------- 平台参数 ----------------

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)

function ProviderTab({ pid }: { pid: string }) {
  const { data, isLoading, error } = useProviderSettings(pid)
  const [draft, setDraft] = useState<Record<string, unknown>>({})
  const [filter, setFilter] = useState('')
  const qc = useQueryClient()
  const toast = useToast()
  useEffect(() => setDraft({}), [pid])

  const save = useMutation({
    mutationFn: (values: Record<string, unknown>) => api<ProviderSettings>(`/api/providers/${encodeURIComponent(pid)}/settings`, { method: 'PUT', body: JSON.stringify({ values }) }),
    onSuccess: (s, values) => {
      qc.setQueryData(['provider-settings', pid], s)
      qc.invalidateQueries({ queryKey: ['provider', pid] })
      setDraft({})
      const n = Object.keys(values).length
      const needRestart = Object.keys(values).filter((k) => s.fields.find((f) => f.key === k)?.restart).length
      toast.ok(needRestart ? `已保存 ${n} 项，其中 ${needRestart} 项重启后生效` : `已保存 ${n} 项，立即生效`)
    },
    onError: (e: Error) => toast.bad(e.message),
  })

  const groups = useMemo(() => {
    const k = filter.trim().toLowerCase()
    const m = new Map<string, SettingField[]>()
    data?.fields
      .filter((f) => !k || f.label.toLowerCase().includes(k) || f.key.includes(k) || f.hint?.toLowerCase().includes(k) || f.group.includes(k))
      .forEach((f) => m.set(f.group, [...(m.get(f.group) ?? []), f]))
    return [...m.entries()]
  }, [data, filter])

  if (isLoading) return <Skeleton className="h-72" />
  if (error || !data) return <Card><Empty icon={<AlertTriangle size={26} />} title="读取平台参数失败" hint={(error as Error)?.message} /></Card>

  const valueOf = (k: string) => (k in draft ? draft[k] : data.values[k])
  const change = (f: SettingField, v: unknown) =>
    setDraft((d) => {
      const n = { ...d }
      if (same(v, data.values[f.key]) || (f.kind === 'secret' && v === '')) delete n[f.key]
      else n[f.key] = v
      return n
    })
  const dirtyKeys = Object.keys(draft)
  const pending = new Set(data.pending_restart)
  const labelOf = (k: string) => data.fields.find((f) => f.key === k)?.label ?? k

  return (
    <div className={cx('space-y-6', dirtyKeys.length > 0 && 'pb-20')}>
      {pending.size > 0 && (
        <div className="flex items-start gap-3 rounded-xl border border-warn/40 bg-warn/[.08] px-4 py-3 text-[13px]">
          <RotateCw size={15} className="mt-0.5 shrink-0 text-warn" />
          <div>
            <div className="font-medium">有 {pending.size} 项修改需要重启服务后生效</div>
            <div className="mt-0.5 text-[12.5px] text-muted">{[...pending].map(labelOf).join('、')}</div>
          </div>
        </div>
      )}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-[12.5px] text-muted">
          写入 <code className="num rounded bg-sunken px-1 py-0.5 text-[12px]">config.json</code> 的 <code className="num rounded bg-sunken px-1 py-0.5 text-[12px]">providers.{pid}</code>；未标注「重启生效」的项保存后立即生效。
        </p>
        <label className="flex h-8 w-full items-center gap-2 rounded-lg border border-line bg-surface px-2.5 focus-within:border-accent sm:w-52">
          <Search size={13} className="text-faint" />
          <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="搜索参数" className="w-full bg-transparent text-[13px] outline-none" />
        </label>
      </div>
      {groups.map(([g, fields]) => (
        <Card key={g}>
          <CardHeader title={g} />
          <div className="divide-y divide-line border-t border-line">
            {fields.map((f) => {
              const disabled = !!f.enabled_by && !valueOf(f.enabled_by)
              const dirty = f.key in draft
              return (
                <SettingRow
                  key={f.key}
                  title={f.label}
                  hint={f.hint}
                  disabled={disabled}
                  stacked={f.kind === 'hours'}
                  badge={
                    <>
                      {dirty && <span className="h-1.5 w-1.5 rounded-full bg-accent" title="未保存" />}
                      {pending.has(f.key) ? <Badge tone="warn">待重启</Badge> : f.restart && <Badge>重启生效</Badge>}
                    </>
                  }
                  control={<FieldControl f={f} value={valueOf(f.key)} onChange={(v) => change(f, v)} disabled={disabled} secretSet={data.secrets_set.includes(f.key)} />}
                />
              )
            })}
          </div>
        </Card>
      ))}
      {!groups.length && <Card><Empty icon={<Search size={24} />} title="没有匹配的参数" /></Card>}

      {dirtyKeys.length > 0 && (
        <div className="pointer-events-none fixed inset-x-0 bottom-5 z-30 flex justify-center px-4 lg:pl-[232px]">
          <div className="pointer-events-auto flex animate-rise items-center gap-3 rounded-2xl border border-ink/80 bg-ink py-2 pl-4 pr-2 text-bg shadow-pop">
            <span className="text-[13px]">
              <b className="num">{dirtyKeys.length}</b> 项未保存
            </span>
            <button onClick={() => setDraft({})} className="inline-flex items-center gap-1 rounded-md px-2.5 py-1 text-[12.5px] hover:bg-bg/10">
              <RotateCcw size={12} /> 还原
            </button>
            <Button size="sm" disabled={save.isPending} onClick={() => save.mutate(draft)} className="!bg-accent !text-accent-ink hover:!bg-accent/90">
              保存
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

// ---------------- 页面 ----------------

export function SettingsPage() {
  const { data: meta } = useMeta()
  const { data: upd } = useUpdateStatus()
  const [params, setParams] = useSearchParams()
  const configurable = (meta?.providers ?? []).filter((p) => p.capabilities?.includes('settings'))
  const tab = params.get('tab') ?? 'gateway'
  const hasUpdate = upd?.state === 'available' || upd?.state === 'ready'
  const tabs = [{ id: 'gateway', name: '网关' }, { id: 'update', name: '软件更新' }, ...configurable.map((p) => ({ id: p.id, name: p.name }))]

  return (
    <div className="max-w-3xl space-y-6">
      <div className="flex max-w-full overflow-x-auto rounded-xl border border-line bg-surface p-1 sm:inline-flex">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setParams(t.id === 'gateway' ? {} : { tab: t.id }, { replace: true })}
            className={cx('relative shrink-0 rounded-lg px-3.5 py-1.5 text-[13px] transition-colors', tab === t.id ? 'bg-ink text-bg' : 'text-muted hover:text-ink')}
          >
            {t.name}
            {t.id === 'update' && hasUpdate && <span className="absolute right-1.5 top-1.5 h-1.5 w-1.5 rounded-full bg-accent" title="有新版本" />}
          </button>
        ))}
      </div>
      {tab === 'gateway' ? <GatewayTab /> : tab === 'update' ? <UpdatePanel /> : <ProviderTab key={tab} pid={tab} />}
    </div>
  )
}
