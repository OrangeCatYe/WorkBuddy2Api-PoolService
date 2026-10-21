import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Check, ChevronDown, FolderOpen, Loader2, Search, ShieldAlert, X } from 'lucide-react'
import { post } from '@/lib/api'
import { fmtTokens } from '@/lib/format'
import type { AgentInfo, AgentModel, AgentPlan, AgentRequest } from '@/types'
import { CodeBlock } from './copy'
import { Dialog, useToast } from './overlay'
import { Badge, Button, cx } from './ui'

const checkbox = 'h-4 w-4 shrink-0 accent-[rgb(var(--accent))]'
const selectCls = 'h-9 w-full rounded-lg border border-line bg-bg px-2.5 text-[13px] outline-none focus:border-accent'

/** Claude Desktop 的模型菜单只接受 claude-<家族>-… 命名的模型，与后端判定一致。 */
const claudeOK = (id: string) => /^[A-Za-z0-9._/-]+$/.test(id) && !/\[1m\]/i.test(id) && /^(anthropic\/)?claude-(sonnet|opus|haiku|fable|mythos)-./i.test(id)

const actionLabel: Record<string, { text: string; tone: 'ok' | 'info' | 'bad' | 'neutral' }> = {
  create: { text: '新建', tone: 'ok' },
  update: { text: '修改', tone: 'info' },
  delete: { text: '删除', tone: 'bad' },
  unchanged: { text: '无变化', tone: 'neutral' },
}

type Step = 'edit' | 'preview' | 'done'

/** 路径展示：太长时截断目录部分，文件名始终完整可见（最有用的是文件名）。 */
export function PathText({ path, className }: { path: string; className?: string }) {
  const i = Math.max(path.lastIndexOf('\\'), path.lastIndexOf('/'))
  const dir = i >= 0 ? path.slice(0, i + 1) : ''
  const base = i >= 0 ? path.slice(i + 1) : path
  return (
    <span className={cx('num flex min-w-0 items-baseline', className)} title={path}>
      <span className="truncate text-faint">{dir}</span>
      <span className="shrink-0">{base}</span>
    </span>
  )
}

export interface ImportContext {
  keyId: string
  keyName: string
  noKeys: boolean
  baseUrl: string
  models: AgentModel[]
  canWrite: boolean
  writeBlock: string
}

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-[12.5px] font-medium">{title}</span>
        {hint && <span className="text-[11.5px] text-faint">{hint}</span>}
      </div>
      {children}
    </div>
  )
}

function Notice({ tone, children }: { tone: 'warn' | 'bad'; children: ReactNode }) {
  const cls = tone === 'warn' ? 'border-warn/30 bg-warn/[.07]' : 'border-bad/30 bg-bad/[.06]'
  const Icon = tone === 'warn' ? ShieldAlert : AlertTriangle
  return (
    <div className={cx('flex items-start gap-2.5 rounded-xl border px-3.5 py-2.5 text-[12.5px]', cls)}>
      <Icon size={15} className={cx('mt-0.5 shrink-0', tone === 'warn' ? 'text-warn' : 'text-bad')} />
      <div className="min-w-0 leading-relaxed text-muted">{children}</div>
    </div>
  )
}

/**
 * 导入向导：选模型 → 预览变更 → 写入。
 * 预览不落盘；写入只在本机可用，远程部署时可以「显示完整配置」自行复制。
 */
export function AgentImportDialog({ agent, ctx, onClose }: { agent: AgentInfo | null; ctx: ImportContext; onClose: () => void }) {
  const qc = useQueryClient()
  const toast = useToast()
  const [info, setInfo] = useState<AgentInfo | null>(agent)
  const [step, setStep] = useState<Step>('edit')
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [q, setQ] = useState('')
  const [wantDefault, setWantDefault] = useState(true)
  const [defaultModel, setDefaultModel] = useState('')
  const [fastModel, setFastModel] = useState('')
  const [opusModel, setOpusModel] = useState('')
  const [sonnetModel, setSonnetModel] = useState('')
  const [profile, setProfile] = useState('')
  const [dir, setDir] = useState('')
  const [advanced, setAdvanced] = useState(false)
  const [plan, setPlan] = useState<AgentPlan | null>(null)
  const [full, setFull] = useState(false)
  const [busy, setBusy] = useState<'' | 'preview' | 'apply' | 'render' | 'detect'>('')
  // 每次打开 / 切换 / 关闭向导都递增；在途请求返回时若序号已变，结果只用于刷新与提示，不再改动当前对话框。
  const seq = useRef(0)

  const isCode = agent?.id === 'claude-code'
  const usable = useMemo(() => ctx.models.filter((m) => agent?.id !== 'claude-desktop' || claudeOK(m.id)), [ctx.models, agent?.id])

  // 打开 / 切换 Agent 时重置
  useEffect(() => {
    seq.current++
    if (!agent) return
    const imported = (agent.imported_models ?? []).filter((id) => ctx.models.some((m) => m.id === id))
    const base = usable.map((m) => m.id)
    const chosen = agent.imported && imported.length && !isCode ? imported.filter((id) => base.includes(id)) : base
    setInfo(agent)
    setStep('edit')
    setQ('')
    setSel(new Set(chosen))
    setWantDefault(true)
    setDefaultModel(isCode ? (imported[0] ?? base[0] ?? '') : (chosen[0] ?? ''))
    setFastModel('')
    setOpusModel('')
    setSonnetModel('')
    setProfile(agent.profile ?? '')
    setDir('')
    setAdvanced(false)
    setPlan(null)
    setFull(false)
    setBusy('')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agent?.id])

  // 模型随密钥变化时，去掉已不可用的选择
  useEffect(() => {
    const ids = new Set(usable.map((m) => m.id))
    setSel((s) => new Set([...s].filter((id) => ids.has(id))))
  }, [usable])

  // 档案 / 目录变化时重新探测
  useEffect(() => {
    if (!agent) return
    if (profile === (agent.profile ?? '') && !dir.trim()) {
      setInfo(agent)
      return
    }
    let stale = false
    const t = window.setTimeout(async () => {
      setBusy('detect')
      try {
        const next = await post<AgentInfo>(`/api/agents/${agent.id}/detect`, { profile, config_dir: dir.trim() })
        if (!stale) setInfo(next)
      } catch (e) {
        if (!stale) toast.bad((e as Error).message)
      } finally {
        if (!stale) setBusy('')
      }
    }, 350)
    return () => {
      stale = true
      window.clearTimeout(t)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [profile, dir])

  const shown = useMemo(() => {
    const k = q.trim().toLowerCase()
    return k ? ctx.models.filter((m) => m.id.toLowerCase().includes(k) || m.name.toLowerCase().includes(k)) : ctx.models
  }, [ctx.models, q])
  const selectable = shown.filter((m) => usable.some((u) => u.id === m.id))
  const allShown = selectable.length > 0 && selectable.every((m) => sel.has(m.id))
  const chosenModels = useMemo(() => ctx.models.filter((m) => sel.has(m.id)), [ctx.models, sel])

  // 默认模型必须在已选范围内
  useEffect(() => {
    if (isCode || !chosenModels.length) return
    if (!chosenModels.some((m) => m.id === defaultModel)) setDefaultModel(chosenModels[0].id)
  }, [chosenModels, defaultModel, isCode])

  if (!agent || !info) return null
  const infoNow = info
  const takeDefault = infoNow.supports_default && (infoNow.needs_default || wantDefault)

  const toggle = (id: string) =>
    setSel((s) => {
      const n = new Set(s)
      n.has(id) ? n.delete(id) : n.add(id)
      return n
    })
  const toggleShown = () =>
    setSel((s) => {
      const n = new Set(s)
      selectable.forEach((m) => (allShown ? n.delete(m.id) : n.add(m.id)))
      return n
    })

  const request = (): AgentRequest => {
    const ids = isCode ? [...new Set([defaultModel, fastModel, opusModel, sonnetModel].filter(Boolean))] : chosenModels.map((m) => m.id)
    return {
      key_id: ctx.keyId,
      models: ids,
      default_model: takeDefault ? defaultModel : '',
      fast_model: isCode ? fastModel : '',
      opus_model: isCode ? opusModel : '',
      sonnet_model: isCode ? sonnetModel : '',
      profile,
      config_dir: dir.trim(),
      base_url: ctx.baseUrl,
    }
  }
  const ready = !infoNow.blocked && (isCode ? !!defaultModel : chosenModels.length > 0)

  async function run(kind: 'preview' | 'render' | 'apply') {
    const my = seq.current
    setBusy(kind)
    try {
      const res = await post<AgentPlan>(`/api/agents/${agent!.id}/${kind}`, request())
      if (kind === 'apply') {
        qc.invalidateQueries({ queryKey: ['agents'] })
        qc.invalidateQueries({ queryKey: ['keys'] })
        toast.ok(`已写入 ${agent!.name}`)
      }
      if (my !== seq.current) return
      setPlan(res)
      if (kind === 'apply') setStep('done')
      else {
        setFull(kind === 'render')
        setStep('preview')
      }
    } catch (e) {
      toast.bad((e as Error).message)
    } finally {
      if (my === seq.current) setBusy('')
    }
  }

  const footer =
    step === 'edit' ? (
      <>
        <Button onClick={onClose}>取消</Button>
        <Button variant="primary" onClick={() => run('preview')} disabled={!ready || !!busy} icon={busy === 'preview' ? <Loader2 size={14} className="animate-spin" /> : undefined}>
          预览变更
        </Button>
      </>
    ) : step === 'preview' ? (
      <>
        <Button onClick={() => setStep('edit')} disabled={!!busy}>
          返回修改
        </Button>
        {!full && (
          <Button onClick={() => run('render')} disabled={!!busy} icon={busy === 'render' ? <Loader2 size={14} className="animate-spin" /> : undefined}>
            显示完整配置
          </Button>
        )}
        <Button
          variant="primary"
          onClick={() => run('apply')}
          disabled={!ctx.canWrite || !!busy || !plan?.files.some((f) => f.action !== 'unchanged')}
          title={ctx.canWrite ? undefined : ctx.writeBlock}
          icon={busy === 'apply' ? <Loader2 size={14} className="animate-spin" /> : <Check size={14} />}
        >
          {plan?.files.some((f) => f.action !== 'unchanged') ? '写入' : '已是最新'}
        </Button>
      </>
    ) : (
      <Button variant="primary" onClick={onClose}>
        完成
      </Button>
    )

  return (
    <Dialog open onClose={onClose} title={`${infoNow.imported ? '更新' : '导入到'} ${agent.name}`} width={660} footer={footer}>
      <div className="max-h-[66vh] space-y-4 overflow-y-auto pr-1">
        {step === 'edit' && (
          <>
            {infoNow.takeover && (
              <Notice tone="warn">
                <b className="font-medium text-ink">这会替换 {agent.name} 当前使用的接口，被改动的原值会记录下来，点「移除」即可还原。</b>
                {infoNow.notes?.map((n) => (
                  <div key={n} className="mt-1">
                    {n}
                  </div>
                ))}
              </Notice>
            )}
            {infoNow.blocked && <Notice tone="bad">{infoNow.blocked}</Notice>}
            {!ctx.canWrite && (
              <Notice tone="warn">
                当前不能直接写入：{ctx.writeBlock}。仍可预览，并用「显示完整配置」复制后手动粘贴。
              </Notice>
            )}

            <div className="rounded-xl border border-line bg-sunken/40 px-3.5 py-2.5 text-[12.5px]">
              <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
                <span className="text-muted">
                  密钥 <b className="font-medium text-ink">{ctx.noKeys ? '将自动创建默认密钥' : ctx.keyName || '未选择'}</b>
                </span>
                <span className="num min-w-0 truncate text-muted">
                  网关 <b className="font-medium text-ink">{ctx.baseUrl}</b>
                </span>
              </div>
              <div className="mt-1.5 space-y-0.5">
                <div className="eyebrow !text-[10.5px]">将写入</div>
                {infoNow.files.map((f) => (
                  <div key={f.path} className="flex items-center gap-2">
                    <PathText path={f.path} className="flex-1 text-[12px]" />
                    <Badge tone={f.exists ? 'info' : 'ok'} className="shrink-0 !text-[10.5px]">
                      {f.exists ? '已存在' : '新建'}
                    </Badge>
                  </div>
                ))}
              </div>
            </div>
            {!infoNow.takeover && (infoNow.notes?.length ?? 0) > 0 && (
              <ul className="space-y-0.5 text-[12px] leading-relaxed text-muted">
                {infoNow.notes!.map((n) => (
                  <li key={n}>· {n}</li>
                ))}
              </ul>
            )}

            {(infoNow.profiles?.length ?? 0) > 1 && (
              <Section title="档案" hint="只写入所选档案，其他档案不受影响">
                <select value={profile} onChange={(e) => setProfile(e.target.value)} className={selectCls}>
                  {infoNow.profiles!.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.label}
                      {p.hint ? `（${p.hint}）` : ''}
                    </option>
                  ))}
                </select>
              </Section>
            )}

            {isCode ? (
              <div className="space-y-3">
                <Section title="主模型" hint="必选，会话默认使用">
                  <select value={defaultModel} onChange={(e) => setDefaultModel(e.target.value)} className={selectCls}>
                    {usable.map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.name || m.id}
                      </option>
                    ))}
                  </select>
                </Section>
                <Section title="Claude 档位映射" hint="Claude Code 的 opus / sonnet / haiku 别名各指向哪个模型；不指定的跟随主模型">
                  <div className="grid gap-3 sm:grid-cols-3">
                    {(
                      [
                        { label: 'Opus', hint: '复杂任务、/model opus', value: opusModel, set: setOpusModel },
                        { label: 'Sonnet', hint: '日常编码、/model sonnet', value: sonnetModel, set: setSonnetModel },
                        { label: 'Haiku', hint: '后台小任务、会话标题', value: fastModel, set: setFastModel },
                      ] as const
                    ).map((t) => (
                      <label key={t.label} className="block space-y-1">
                        <span className="text-[12px] font-medium text-ink">{t.label}</span>
                        <select value={t.value} onChange={(e) => t.set(e.target.value)} className={selectCls}>
                          <option value="">跟随主模型</option>
                          {usable.map((m) => (
                            <option key={m.id} value={m.id}>
                              {m.name || m.id}
                            </option>
                          ))}
                        </select>
                        <span className="block text-[11.5px] text-faint">{t.hint}</span>
                      </label>
                    ))}
                  </div>
                </Section>
              </div>
            ) : (
              <Section title="模型" hint={`已选 ${chosenModels.length} / ${usable.length}`}>
                <label className="flex h-9 items-center gap-2 rounded-lg border border-line bg-bg px-3 focus-within:border-accent">
                  <Search size={14} className="shrink-0 text-faint" />
                  <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索模型名称或 ID" className="w-full min-w-0 bg-transparent text-[13px] outline-none" />
                  {q && (
                    <button onClick={() => setQ('')} className="text-faint hover:text-ink" aria-label="清空搜索">
                      <X size={13} />
                    </button>
                  )}
                </label>
                <div className="overflow-hidden rounded-xl border border-line">
                  <div className="flex items-center gap-2.5 border-b border-line bg-sunken/50 px-3.5 py-2">
                    <input type="checkbox" className={checkbox} checked={allShown} onChange={toggleShown} disabled={!selectable.length} aria-label="全选当前列表" />
                    <span className="text-[12.5px] text-muted">{q.trim() ? `筛选出 ${shown.length} 个` : `共 ${ctx.models.length} 个`}</span>
                    <button onClick={() => setSel(new Set())} disabled={!sel.size} className="ml-auto text-[12px] text-faint hover:text-ink disabled:opacity-40">
                      清空
                    </button>
                  </div>
                  <ul className="max-h-[230px] divide-y divide-line overflow-y-auto">
                    {shown.map((m) => {
                      const ok = usable.some((u) => u.id === m.id)
                      return (
                        <li key={m.id}>
                          <label className={cx('flex items-center gap-3 px-3.5 py-2', ok ? 'cursor-pointer hover:bg-sunken/40' : 'cursor-not-allowed opacity-50')}>
                            <input type="checkbox" className={checkbox} checked={sel.has(m.id)} onChange={() => toggle(m.id)} disabled={!ok} />
                            <div className="min-w-0 flex-1">
                              <div className="truncate text-[13px] font-medium">{m.name || m.id}</div>
                              <div className="num truncate text-[11.5px] text-faint">
                                {m.id}
                                {!ok && ' · 不是 claude-* 命名，Claude Desktop 无法使用'}
                              </div>
                            </div>
                            <div className="flex shrink-0 items-center gap-1">
                              {m.reasoning && <Badge tone="accent" className="!px-1 !py-0 !text-[10.5px]">思考</Badge>}
                              {m.vision && <Badge tone="info" className="!px-1 !py-0 !text-[10.5px]">图片</Badge>}
                              {!!m.context && <span className="num w-[44px] text-right text-[11.5px] text-faint">{fmtTokens(m.context)}</span>}
                            </div>
                          </label>
                        </li>
                      )
                    })}
                    {!shown.length && <li className="px-4 py-8 text-center text-[13px] text-muted">{ctx.models.length ? '没有匹配的模型' : '模型目录为空：请先添加账号并刷新模型'}</li>}
                  </ul>
                </div>
              </Section>
            )}

            {infoNow.supports_default && !infoNow.needs_default && (
              <div className="flex flex-wrap items-center gap-3">
                <label className="flex items-center gap-2 text-[13px]">
                  <input type="checkbox" className={checkbox} checked={wantDefault} onChange={(e) => setWantDefault(e.target.checked)} />
                  设为默认模型
                </label>
                {wantDefault && (
                  <select value={defaultModel} onChange={(e) => setDefaultModel(e.target.value)} className={cx(selectCls, 'max-w-[300px] flex-1')} disabled={!chosenModels.length}>
                    {chosenModels.map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.name || m.id}
                      </option>
                    ))}
                  </select>
                )}
              </div>
            )}
            <div>
              <button onClick={() => setAdvanced(!advanced)} className="flex items-center gap-1.5 text-[12.5px] text-muted hover:text-ink">
                <ChevronDown size={14} className={cx('transition-transform', advanced && 'rotate-180')} />
                找不到配置？手动指定配置目录
              </button>
              {advanced && (
                <div className="mt-2 space-y-1.5">
                  <label className="flex h-9 items-center gap-2 rounded-lg border border-line bg-bg px-3 focus-within:border-accent">
                    <FolderOpen size={14} className="shrink-0 text-faint" />
                    <input value={dir} onChange={(e) => setDir(e.target.value)} placeholder="配置所在的目录，例如 D:\Apps\codex-home" className="num w-full min-w-0 bg-transparent text-[12.5px] outline-none" />
                    {busy === 'detect' && <Loader2 size={13} className="animate-spin text-faint" />}
                  </label>
                  <p className="text-[11.5px] text-faint">软件装在哪里不影响导入，只需要告诉我配置目录；只会在该目录下写固定文件名的文件。留空使用自动检测的位置。</p>
                </div>
              )}
            </div>
          </>
        )}

        {step !== 'edit' && plan && (
          <>
            <div className="rounded-xl border border-line bg-sunken/40 px-3.5 py-2.5 text-[12.5px] leading-relaxed">
              <div className="flex flex-wrap gap-x-5 gap-y-1 text-muted">
                <span>
                  模型 <b className="num font-semibold text-ink">{plan.imported?.length ?? 0}</b>
                </span>
                <span>
                  密钥{' '}
                  <b className="font-medium text-ink">
                    {plan.key?.name}
                    {plan.key?.will_create && '（预览时尚未创建，写入时自动创建）'}
                    {plan.key?.created && '（已自动创建）'}
                  </b>
                </span>
                {plan.base_url && <span className="num min-w-0 truncate">网关 {plan.base_url}</span>}
              </div>
              {plan.imported && plan.imported.length > 0 && <div className="num mt-1 break-words text-[11.5px] text-faint">{plan.imported.join('、')}</div>}
            </div>

            {plan.warnings?.map((w) => (
              <Notice key={w} tone="warn">
                {w}
              </Notice>
            ))}
            {plan.skipped && plan.skipped.length > 0 && (
              <Notice tone="warn">
                <div className="font-medium text-ink">有 {plan.skipped.length} 个模型被跳过</div>
                <ul className="mt-1 space-y-0.5">
                  {plan.skipped.map((s) => (
                    <li key={s.id}>
                      <span className="num text-ink">{s.id}</span>：{s.reason}
                    </li>
                  ))}
                </ul>
              </Notice>
            )}

            {step === 'done' ? (
              <div className="space-y-2">
                <div className="flex items-center gap-2 text-[13.5px] font-medium">
                  <Check size={16} className="text-ok" />
                  {plan.written?.length ? `已写入 ${plan.written.length} 个文件` : '配置已是最新，没有改动'}
                </div>
                <ul className="space-y-0.5">
                  {plan.written?.map((p) => (
                    <li key={p}>
                      <PathText path={p} className="text-[12px]" />
                    </li>
                  ))}
                </ul>
                {plan.backup_dir && <p className="text-[12px] text-muted">原配置已备份到 <span className="num break-all">{plan.backup_dir}</span></p>}
                {plan.restart && <p className="rounded-lg bg-accent/[.08] px-3 py-2 text-[12.5px]">下一步：{plan.restart}</p>}
              </div>
            ) : (
              plan.files.map((f) => {
                const a = actionLabel[f.action] ?? actionLabel.update
                return (
                  <div key={f.path} className="space-y-1.5">
                    <div className="flex items-center gap-2">
                      <Badge tone={a.tone}>{a.text}</Badge>
                      <PathText path={f.path} className="flex-1 text-[12px]" />
                    </div>
                    {f.snippet && <CodeBlock code={f.snippet} lang={`${f.lang ?? ''}${full ? ' · 完整内容，含密钥' : ' · 将写入的受管部分，密钥已遮掩'}`} />}
                  </div>
                )
              })
            )}
          </>
        )}
      </div>
    </Dialog>
  )
}

/** 移除：只删除本工具写入的内容，并还原被改过的原值。 */
export function AgentRemoveDialog({ agent, onClose }: { agent: AgentInfo | null; onClose: () => void }) {
  const qc = useQueryClient()
  const toast = useToast()
  const [dir, setDir] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => setDir(''), [agent?.id])
  if (!agent) return null

  async function run() {
    setBusy(true)
    try {
      const res = await post<AgentPlan>(`/api/agents/${agent!.id}/remove`, { profile: agent!.profile ?? '', config_dir: dir.trim() })
      qc.invalidateQueries({ queryKey: ['agents'] })
      toast.ok(res.written?.length ? `已从 ${agent!.name} 移除` : '没有需要改动的内容')
      res.warnings?.forEach((w) => toast.bad(w))
      onClose()
    } catch (e) {
      toast.bad((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open
      onClose={onClose}
      title={`从 ${agent.name} 移除`}
      width={460}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="danger" onClick={run} disabled={busy} icon={busy ? <Loader2 size={14} className="animate-spin" /> : undefined}>
            移除
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-[13.5px] text-muted">
        <p>只会删除本工具写入的供应商和模型，其余配置原样保留；写入前改过的值会还原成原来的样子。写入前的备份仍保留在数据目录里。</p>
        {agent.takeover && <p>移除后 {agent.name} 会回到导入之前使用的接口。</p>}
        <label className="block">
          <span className="text-[12.5px] font-medium text-ink">配置目录（仅当导入时手动指定过才需要填）</span>
          <input value={dir} onChange={(e) => setDir(e.target.value)} placeholder="留空使用自动检测的位置" className="num mt-1 h-9 w-full rounded-lg border border-line bg-bg px-3 text-[12.5px] text-ink outline-none focus:border-accent" />
        </label>
      </div>
    </Dialog>
  )
}
