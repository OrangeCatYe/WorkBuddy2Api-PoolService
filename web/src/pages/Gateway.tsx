import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Eye, EyeOff, KeyRound, MoreHorizontal, Plus, RotateCw, Trash2, Undo2 } from 'lucide-react'
import { api, post } from '@/lib/api'
import { useConnect, useKeys, useModels } from '@/lib/queries'
import { fmtNum, fmtRelative, fmtTokens, pct } from '@/lib/format'
import { CodeBlock, CopyButton } from '@/components/copy'
import { Dialog, Segmented, Switch, useToast } from '@/components/overlay'
import { Badge, Button, Card, CardHeader, Empty, Meter, Skeleton, cx } from '@/components/ui'
import type { ApiKey, KeySpec } from '@/types'

const mask = (s: string) => (s.length > 14 ? `${s.slice(0, 10)}••••${s.slice(-4)}` : '••••••')

// ---------------- 接入向导 ----------------

type Client = 'ttswitch' | 'claude' | 'codex' | 'curl'

function ConnectCard({ keys }: { keys: ApiKey[] }) {
  const { data: info } = useConnect()
  const [client, setClient] = useState<Client>('ttswitch')
  const [keyId, setKeyId] = useState('')
  const usable = keys.filter((k) => k.enabled)
  const key = usable.find((k) => k.id === keyId) ?? usable[0]
  const secret = key?.secret ?? '<API_KEY>'
  const openai = info?.openai ?? ''
  const anthropic = info?.anthropic ?? ''

  const snippets: Record<Client, { lang: string; code: string; hint: string }> = {
    ttswitch: {
      lang: '供应商设置',
      hint: '在 ttswitch 中新增「自定义 OpenAI 兼容供应商」，保存后点「拉取模型」，上下文、工具、思考档位会自动识别。',
      code: `Base URL   ${openai}\nAPI Key    ${secret}\nAPI 格式   Chat Completions`,
    },
    claude: {
      lang: 'PowerShell / Bash',
      hint: 'Claude Code 走 Anthropic Messages 协议，模型名用本服务的模型 ID（如 glm-5.2）。',
      code: `# PowerShell\n$env:ANTHROPIC_BASE_URL="${anthropic}"\n$env:ANTHROPIC_AUTH_TOKEN="${secret}"\n$env:ANTHROPIC_MODEL="glm-5.2"\nclaude\n\n# Bash\nexport ANTHROPIC_BASE_URL="${anthropic}"\nexport ANTHROPIC_AUTH_TOKEN="${secret}"\nexport ANTHROPIC_MODEL="glm-5.2"`,
    },
    codex: {
      lang: '~/.codex/config.toml',
      hint: 'Codex CLI 走 Responses 协议；本服务无状态，Codex 默认发送完整上下文，无需额外设置。',
      code: `model = "glm-5.2"\nmodel_provider = "poolservice"\n\n[model_providers.poolservice]\nname = "PoolService"\nbase_url = "${openai}"\nwire_api = "responses"\nenv_key = "POOLSVC_API_KEY"\n\n# 环境变量：POOLSVC_API_KEY=${secret}`,
    },
    curl: {
      lang: 'curl',
      hint: '三种协议任选：/v1/chat/completions、/v1/messages、/v1/responses。',
      code: `curl ${openai}/chat/completions \\\n  -H "Authorization: Bearer ${secret}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"glm-5.2","stream":true,"messages":[{"role":"user","content":"你好"}]}'`,
    },
  }
  const s = snippets[client]

  return (
    <Card>
      <CardHeader title="接入" hint="同一个密钥可用于 OpenAI Chat、Anthropic Messages、OpenAI Responses 三种协议" />
      <div className="grid gap-3 border-t border-line px-5 py-4 sm:grid-cols-2">
        {[
          ['OpenAI 兼容 Base URL', openai, 'Chat Completions · Responses'],
          ['Anthropic Base URL', anthropic, 'Messages · count_tokens'],
        ].map(([label, url, sub]) => (
          <div key={label} className="rounded-xl border border-line bg-sunken/50 px-3.5 py-2.5">
            <div className="flex items-center justify-between">
              <span className="eyebrow">{label}</span>
              <CopyButton text={url} />
            </div>
            <div className="num mt-0.5 truncate text-[13.5px] font-medium">{url || '—'}</div>
            <div className="text-[11.5px] text-faint">{sub}</div>
          </div>
        ))}
      </div>
      <div className="space-y-3 border-t border-line px-5 py-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <Segmented
            value={client}
            onChange={setClient}
            options={[
              { value: 'ttswitch', label: 'ttswitch' },
              { value: 'claude', label: 'Claude Code' },
              { value: 'codex', label: 'Codex CLI' },
              { value: 'curl', label: 'curl' },
            ]}
          />
          {usable.length > 1 && (
            <select value={key?.id} onChange={(e) => setKeyId(e.target.value)} className="h-8 rounded-lg border border-line bg-surface px-2 text-[12.5px] outline-none focus:border-accent">
              {usable.map((k) => (
                <option key={k.id} value={k.id}>
                  使用密钥：{k.name}
                </option>
              ))}
            </select>
          )}
        </div>
        <p className="text-[12.5px] text-muted">{s.hint}</p>
        <CodeBlock code={s.code} lang={s.lang} />
      </div>
    </Card>
  )
}

// ---------------- Key 编辑 ----------------

const emptySpec: KeySpec = { name: '', note: '', models: [], rpm: 0, credit_budget: 0, daily_credit_budget: 0, expires_at: null }

function NumField({ label, hint, value, onChange, unit }: { label: string; hint: string; value: number; onChange: (n: number) => void; unit: string }) {
  return (
    <label className="block">
      <span className="text-[12.5px] font-medium">{label}</span>
      <div className="mt-1 flex h-9 items-center rounded-lg border border-line bg-bg focus-within:border-accent">
        <input
          type="number"
          min={0}
          value={value || ''}
          placeholder="不限"
          onChange={(e) => onChange(Math.max(0, Number(e.target.value) || 0))}
          className="num h-full w-full bg-transparent px-3 text-[13px] outline-none"
        />
        <span className="pr-3 text-[12px] text-faint">{unit}</span>
      </div>
      <span className="mt-1 block text-[11.5px] text-faint">{hint}</span>
    </label>
  )
}

function KeyDialog({ open, onClose, editing }: { open: boolean; onClose: () => void; editing?: ApiKey }) {
  const [spec, setSpec] = useState<KeySpec>(emptySpec)
  const [created, setCreated] = useState<ApiKey | null>(null)
  const { data: models } = useModels()
  const qc = useQueryClient()
  const toast = useToast()

  useEffect(() => {
    if (!open) return
    setCreated(null)
    setSpec(
      editing
        ? {
            name: editing.name,
            note: editing.note ?? '',
            models: editing.models ?? [],
            rpm: editing.rpm ?? 0,
            credit_budget: editing.credit_budget ?? 0,
            daily_credit_budget: editing.daily_credit_budget ?? 0,
            expires_at: editing.expires_at ?? null,
            enabled: editing.enabled,
          }
        : emptySpec,
    )
  }, [open, editing])

  const m = useMutation({
    mutationFn: (s: KeySpec) =>
      editing ? api<ApiKey>(`/api/keys/${editing.id}`, { method: 'PUT', body: JSON.stringify(s) }) : post<ApiKey>('/api/keys', s),
    onSuccess: (k) => {
      qc.invalidateQueries({ queryKey: ['keys'] })
      if (editing) {
        toast.ok('已保存')
        onClose()
      } else setCreated(k)
    },
    onError: (e: Error) => toast.bad(e.message),
  })

  const modelIds = useMemo(() => [...new Set(models?.models.map((x) => x.id.replace(/\(.*\)$/, '')) ?? [])].sort(), [models])
  const toggleModel = (id: string) =>
    setSpec((s) => ({ ...s, models: s.models.includes(id) ? s.models.filter((x) => x !== id) : [...s.models, id] }))
  const expires = spec.expires_at ? spec.expires_at.slice(0, 10) : ''

  if (created) {
    return (
      <Dialog
        open={open}
        onClose={onClose}
        title="密钥已创建"
        width={520}
        footer={
          <Button variant="primary" onClick={onClose}>
            完成
          </Button>
        }
      >
        <p className="text-[13px] text-muted">「{created.name}」已可使用。之后也能在列表中再次查看与复制。</p>
        <div className="mt-3 flex items-center gap-2 rounded-xl border border-accent/40 bg-accent/[.06] px-3.5 py-3">
          <code className="num flex-1 break-all text-[13px]">{created.secret}</code>
          <CopyButton text={created.secret} label="复制" />
        </div>
      </Dialog>
    )
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={editing ? `编辑密钥 · ${editing.name}` : '新建密钥'}
      width={600}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" disabled={!spec.name.trim() || m.isPending} onClick={() => m.mutate(spec)}>
            {editing ? '保存' : '创建'}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-[1fr_160px]">
          <label className="block">
            <span className="text-[12.5px] font-medium">名称</span>
            <input
              autoFocus
              value={spec.name}
              onChange={(e) => setSpec({ ...spec, name: e.target.value })}
              placeholder="如：ttswitch / Claude Code / 同事 A"
              className="mt-1 h-9 w-full rounded-lg border border-line bg-bg px-3 text-[13px] outline-none focus:border-accent"
            />
          </label>
          <label className="block">
            <span className="text-[12.5px] font-medium">过期日期</span>
            <input
              type="date"
              value={expires}
              onChange={(e) => setSpec({ ...spec, expires_at: e.target.value ? new Date(`${e.target.value}T23:59:59`).toISOString() : null })}
              className="mt-1 h-9 w-full rounded-lg border border-line bg-bg px-3 text-[13px] outline-none focus:border-accent"
            />
          </label>
        </div>

        <div className="grid gap-3 sm:grid-cols-3">
          <NumField label="每分钟请求" hint="RPM 上限" unit="次/分" value={spec.rpm} onChange={(n) => setSpec({ ...spec, rpm: Math.floor(n) })} />
          <NumField label="每日积分" hint="自然日重置" unit="积分" value={spec.daily_credit_budget} onChange={(n) => setSpec({ ...spec, daily_credit_budget: n })} />
          <NumField label="累计积分" hint="用完即停，可重置用量" unit="积分" value={spec.credit_budget} onChange={(n) => setSpec({ ...spec, credit_budget: n })} />
        </div>

        <div>
          <div className="flex items-baseline justify-between">
            <span className="text-[12.5px] font-medium">可用模型</span>
            <span className="text-[11.5px] text-faint">{spec.models.length ? `已选 ${spec.models.length} 条规则` : '未选择 = 全部模型'}</span>
          </div>
          <div className="mt-1.5 flex max-h-40 flex-wrap gap-1.5 overflow-y-auto rounded-lg border border-line bg-bg p-2">
            {modelIds.map((id) => (
              <button
                key={id}
                type="button"
                onClick={() => toggleModel(id)}
                className={cx('num rounded-md border px-2 py-0.5 text-[12px]', spec.models.includes(id) ? 'border-accent bg-accent/10 text-ink' : 'border-line text-muted hover:text-ink')}
              >
                {id}
              </button>
            ))}
            {!modelIds.length && <span className="text-[12px] text-faint">模型目录为空</span>}
          </div>
          <input
            value={spec.models.filter((x) => !modelIds.includes(x)).join(', ')}
            onChange={(e) => {
              const custom = e.target.value.split(/[,\s]+/).filter(Boolean)
              setSpec({ ...spec, models: [...spec.models.filter((x) => modelIds.includes(x)), ...custom] })
            }}
            placeholder="通配规则，逗号分隔：glm-*, workbuddy/*"
            className="num mt-1.5 h-8 w-full rounded-lg border border-line bg-bg px-3 text-[12px] outline-none focus:border-accent"
          />
        </div>

        <label className="block">
          <span className="text-[12.5px] font-medium">备注</span>
          <input
            value={spec.note}
            onChange={(e) => setSpec({ ...spec, note: e.target.value })}
            className="mt-1 h-9 w-full rounded-lg border border-line bg-bg px-3 text-[13px] outline-none focus:border-accent"
          />
        </label>
      </div>
    </Dialog>
  )
}

// ---------------- Key 列表 ----------------

function BudgetMeter({ used, budget, label }: { used: number; budget?: number; label: string }) {
  if (!budget) return null
  const p = pct(used, budget)
  return (
    <div className="min-w-[110px]">
      <div className="num flex justify-between text-[11px] text-faint">
        <span>{label}</span>
        <span>
          {fmtNum(used)} / {fmtNum(budget)}
        </span>
      </div>
      <Meter className="mt-1" value={p} tone={p >= 90 ? 'bad' : p >= 70 ? 'warn' : 'accent'} />
    </div>
  )
}

function KeyRow({ k, onEdit }: { k: ApiKey; onEdit: () => void }) {
  const [show, setShow] = useState(false)
  const [menu, setMenu] = useState(false)
  const [confirm, setConfirm] = useState<'delete' | 'rotate' | null>(null)
  const qc = useQueryClient()
  const toast = useToast()
  const refresh = () => qc.invalidateQueries({ queryKey: ['keys'] })
  const act = useMutation({
    mutationFn: async (a: 'toggle' | 'rotate' | 'reset' | 'delete') => {
      if (a === 'delete') return api(`/api/keys/${k.id}`, { method: 'DELETE' })
      if (a === 'rotate') return post(`/api/keys/${k.id}/rotate`)
      if (a === 'reset') return post(`/api/keys/${k.id}/reset-usage`)
      return api(`/api/keys/${k.id}`, {
        method: 'PUT',
        body: JSON.stringify({ ...k, models: k.models ?? [], note: k.note ?? '', rpm: k.rpm ?? 0, credit_budget: k.credit_budget ?? 0, daily_credit_budget: k.daily_credit_budget ?? 0, enabled: !k.enabled }),
      })
    },
    onSuccess: (_, a) => {
      refresh()
      toast.ok({ toggle: k.enabled ? '已停用' : '已启用', rotate: '已生成新密钥，旧密钥立即失效', reset: '用量已清零', delete: '已删除' }[a])
    },
    onError: (e: Error) => toast.bad(e.message),
  })
  const expired = k.expires_at && new Date(k.expires_at).getTime() < Date.now()
  const u = k.usage

  return (
    <li className={cx('px-5 py-4', !k.enabled && 'opacity-55')}>
      <div className="flex items-start gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="font-medium">{k.name}</span>
            {k.builtin && <Badge>内置</Badge>}
            {expired && <Badge tone="bad">已过期</Badge>}
            {k.models?.length ? <Badge tone="info">限 {k.models.length} 条模型规则</Badge> : null}
            {k.rpm ? <Badge>{k.rpm} RPM</Badge> : null}
            {k.expires_at && !expired && <Badge>{fmtRelative(k.expires_at)}过期</Badge>}
          </div>
          <div className="mt-1 flex items-center gap-1">
            <code className="num truncate text-[12px] text-faint">{show ? k.secret : mask(k.secret)}</code>
            <button onClick={() => setShow(!show)} className="rounded p-0.5 text-faint hover:text-ink" aria-label="显示密钥">
              {show ? <EyeOff size={13} /> : <Eye size={13} />}
            </button>
            <CopyButton text={k.secret} />
          </div>
          {k.note && <div className="mt-0.5 truncate text-[12px] text-faint">{k.note}</div>}
        </div>
        <div className="relative flex shrink-0 items-center gap-2">
          {!k.builtin && (
            <>
              <Switch checked={k.enabled} onChange={() => act.mutate('toggle')} />
              <button onClick={() => setMenu(!menu)} className="rounded-md p-1.5 text-faint hover:bg-sunken hover:text-ink" aria-label="更多">
                <MoreHorizontal size={16} />
              </button>
            </>
          )}
          {menu && (
            <>
              <div className="fixed inset-0 z-10" onClick={() => setMenu(false)} />
              <div className="absolute right-0 top-9 z-20 w-36 animate-fade rounded-xl border border-line bg-surface py-1 shadow-pop">
                {[
                  { id: 'edit', label: '编辑', icon: <KeyRound size={13} /> },
                  { id: 'reset', label: '清零用量', icon: <Undo2 size={13} /> },
                  { id: 'rotate', label: '重新生成', icon: <RotateCw size={13} /> },
                  { id: 'delete', label: '删除', icon: <Trash2 size={13} />, danger: true },
                ].map((x) => (
                  <button
                    key={x.id}
                    onClick={() => {
                      setMenu(false)
                      if (x.id === 'edit') onEdit()
                      else if (x.id === 'reset') act.mutate('reset')
                      else setConfirm(x.id as 'delete' | 'rotate')
                    }}
                    className={cx('flex w-full items-center gap-2 px-3 py-1.5 text-left text-[13px] hover:bg-sunken', x.danger && 'text-bad')}
                  >
                    {x.icon}
                    {x.label}
                  </button>
                ))}
              </div>
            </>
          )}
        </div>
      </div>

      <div className="mt-3 grid grid-cols-2 gap-x-6 gap-y-3 rounded-xl bg-sunken/50 px-4 py-2.5 sm:grid-cols-[repeat(4,minmax(0,1fr))_minmax(0,1.6fr)]">
        {[
          ['请求', <>{fmtNum(u?.requests ?? 0)}{u?.failures ? <span className="text-bad"> · 失败 {u.failures}</span> : null}</>],
          ['Token', fmtTokens((u?.prompt_tokens ?? 0) + (u?.completion_tokens ?? 0)) || '0'],
          ['累计积分', fmtNum(u?.credit ?? 0)],
          ['最近使用', fmtRelative(u?.last_used_at)],
        ].map(([label, v], i) => (
          <div key={i} className="min-w-0">
            <div className="text-[11px] text-faint">{label}</div>
            <div className="num truncate text-[13px]">{v}</div>
          </div>
        ))}
        <div className="col-span-2 space-y-1.5 self-center sm:col-span-1">
          <BudgetMeter label="今日积分" used={u?.day_credit ?? 0} budget={k.daily_credit_budget} />
          <BudgetMeter label="累计积分" used={u?.credit ?? 0} budget={k.credit_budget} />
          {!k.daily_credit_budget && !k.credit_budget && <span className="text-[12px] text-faint">{k.builtin ? '不受限额约束' : '无积分限额'}</span>}
        </div>
      </div>

      <Dialog
        open={!!confirm}
        onClose={() => setConfirm(null)}
        title={confirm === 'delete' ? `删除密钥 · ${k.name}` : `重新生成 · ${k.name}`}
        width={420}
        footer={
          <>
            <Button onClick={() => setConfirm(null)}>取消</Button>
            <Button
              variant="danger"
              onClick={() => {
                act.mutate(confirm!)
                setConfirm(null)
              }}
            >
              确认
            </Button>
          </>
        }
      >
        <p className="text-[13.5px] text-muted">{confirm === 'delete' ? '删除后使用该密钥的客户端会立即收到 401。' : '旧密钥立即失效，需要把新密钥更新到客户端。'}</p>
      </Dialog>
    </li>
  )
}

export function GatewayPage() {
  const { data: keys, isLoading } = useKeys()
  const [dialog, setDialog] = useState<{ open: boolean; editing?: ApiKey }>({ open: false })
  const list = keys ?? []

  return (
    <div className="space-y-6">
      <ConnectCard keys={list} />
      <Card className="overflow-hidden">
        <CardHeader
          title="API 密钥"
          hint="为不同客户端或使用者分配独立密钥，按密钥限制模型、速率与积分"
          actions={
            <Button variant="primary" icon={<Plus size={15} />} onClick={() => setDialog({ open: true })}>
              新建密钥
            </Button>
          }
        />
        {isLoading ? (
          <div className="px-5 pb-5">
            <Skeleton className="h-14" />
          </div>
        ) : list.length ? (
          <ul className="divide-y divide-line border-t border-line">
            {list.map((k) => (
              <KeyRow key={k.id} k={k} onEdit={() => setDialog({ open: true, editing: k })} />
            ))}
          </ul>
        ) : (
          <Empty
            icon={<KeyRound size={28} />}
            title="还没有密钥"
            hint="创建一个密钥，填到 ttswitch、Claude Code 或 Codex 中即可开始使用。"
            action={
              <Button variant="primary" icon={<Plus size={15} />} onClick={() => setDialog({ open: true })}>
                新建密钥
              </Button>
            }
          />
        )}
      </Card>
      <KeyDialog open={dialog.open} editing={dialog.editing} onClose={() => setDialog({ open: false })} />
    </div>
  )
}
