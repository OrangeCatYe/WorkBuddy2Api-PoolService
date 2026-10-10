import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ExternalLink, FileJson, Globe2, Loader2, ShieldCheck } from 'lucide-react'
import { api, post } from '@/lib/api'
import { useProviderDetails } from '@/lib/queries'
import type { LoginMethod, LoginSession, LoginStatus } from '@/types'
import { Dialog, useToast } from './overlay'
import { Button, cx } from './ui'

type Phase = { kind: 'idle' } | { kind: 'starting' } | { kind: 'waiting'; session: LoginSession; message?: string } | { kind: 'done'; status: LoginStatus }

const methodIcon = (id: string) =>
  id.startsWith('import') ? <FileJson size={16} /> : id.includes('global') ? <Globe2 size={16} /> : <ShieldCheck size={16} />

/**
 * 添加账号：平台与登录方式完全由后端能力声明驱动。
 * OAuth 类返回 auth_url → 新窗口打开并每 3 秒轮询；表单类同步返回结果。
 */
export function AddAccountDialog({ open, onClose, defaultProvider }: { open: boolean; onClose: () => void; defaultProvider?: string }) {
  const details = useProviderDetails().filter((d) => d.login_methods?.length)
  const [pid, setPid] = useState('')
  const [method, setMethod] = useState<LoginMethod | null>(null)
  const [fields, setFields] = useState<Record<string, string>>({})
  const [phase, setPhase] = useState<Phase>({ kind: 'idle' })
  const timer = useRef<number>()
  const qc = useQueryClient()
  const toast = useToast()

  const current = details.find((d) => d.meta.id === pid) ?? details[0]

  useEffect(() => {
    if (!open) return
    setPid(defaultProvider && details.some((d) => d.meta.id === defaultProvider) ? defaultProvider : (details[0]?.meta.id ?? ''))
    setPhase({ kind: 'idle' })
    setFields({})
  }, [open])

  useEffect(() => {
    setMethod(current?.login_methods?.[0] ?? null)
  }, [current?.meta.id])

  useEffect(() => () => window.clearInterval(timer.current), [])

  function finish(st: LoginStatus) {
    window.clearInterval(timer.current)
    setPhase({ kind: 'done', status: st })
    if (st.state === 'success') {
      toast.ok(st.message ?? '账号已添加')
      qc.invalidateQueries({ queryKey: ['accounts'] })
      qc.invalidateQueries({ queryKey: ['overview'] })
      qc.invalidateQueries({ queryKey: ['models'] })
    }
  }

  async function start() {
    if (!current || !method) return
    setPhase({ kind: 'starting' })
    try {
      const res = await post<{ session?: LoginSession; status?: LoginStatus }>(`/api/providers/${current.meta.id}/login`, {
        method: method.id,
        params: fields,
      })
      if (res.status) return finish(res.status)
      if (!res.session) throw new Error('平台未返回登录会话')
      const session = res.session
      if (session.auth_url) window.open(session.auth_url, '_blank', 'noopener')
      setPhase({ kind: 'waiting', session })
      timer.current = window.setInterval(async () => {
        try {
          const st = await api<LoginStatus>(`/api/providers/${current.meta.id}/login/${encodeURIComponent(session.id)}`)
          if (st.state === 'pending') setPhase({ kind: 'waiting', session, message: st.message })
          else finish(st)
        } catch (e) {
          finish({ state: 'failed', message: String((e as Error).message) })
        }
      }, 3000)
    } catch (e) {
      setPhase({ kind: 'idle' })
      toast.bad((e as Error).message)
    }
  }

  function close() {
    window.clearInterval(timer.current)
    onClose()
  }

  const busy = phase.kind === 'starting' || phase.kind === 'waiting'

  return (
    <Dialog
      open={open}
      onClose={close}
      title="添加账号"
      width={520}
      footer={
        phase.kind === 'done' ? (
          <>
            <Button onClick={() => setPhase({ kind: 'idle' })}>继续添加</Button>
            <Button variant="primary" onClick={close}>
              完成
            </Button>
          </>
        ) : (
          <>
            <Button onClick={close}>取消</Button>
            <Button variant="primary" onClick={start} disabled={!method || busy || (method.fields ?? []).some((f) => !fields[f.key]?.trim())}>
              {busy && <Loader2 size={14} className="animate-spin" />}
              {method?.fields?.length ? '提交' : '开始授权'}
            </Button>
          </>
        )
      }
    >
      {!details.length ? (
        <p className="py-6 text-center text-[13px] text-muted">当前没有支持在面板添加账号的平台。</p>
      ) : (
        <div className="space-y-4">
          {details.length > 1 && (
            <div>
              <div className="eyebrow mb-1.5">平台</div>
              <div className="flex flex-wrap gap-1.5">
                {details.map((d) => (
                  <button
                    key={d.meta.id}
                    disabled={busy}
                    onClick={() => setPid(d.meta.id)}
                    className={cx('rounded-lg border px-3 py-1.5 text-[13px]', d.meta.id === current?.meta.id ? 'border-ink bg-ink text-bg' : 'border-line text-muted hover:text-ink')}
                  >
                    {d.meta.name}
                  </button>
                ))}
              </div>
            </div>
          )}

          <div>
            <div className="eyebrow mb-1.5">方式</div>
            <div className="grid gap-2">
              {current?.login_methods?.map((m) => (
                <button
                  key={m.id}
                  disabled={busy}
                  onClick={() => setMethod(m)}
                  className={cx(
                    'flex items-start gap-3 rounded-xl border px-3.5 py-3 text-left transition-colors',
                    method?.id === m.id ? 'border-accent bg-accent/[.06]' : 'border-line hover:border-faint',
                  )}
                >
                  <span className={cx('mt-0.5', method?.id === m.id ? 'text-accent' : 'text-faint')}>{methodIcon(m.id)}</span>
                  <span>
                    <span className="block text-[13.5px] font-medium">{m.name}</span>
                    {m.description && <span className="mt-0.5 block text-[12px] text-muted">{m.description}</span>}
                  </span>
                </button>
              ))}
            </div>
          </div>

          {method?.fields?.map((f) => (
            <label key={f.key} className="block">
              <span className="eyebrow mb-1.5 block">{f.label}</span>
              {f.kind === 'textarea' ? (
                <textarea
                  value={fields[f.key] ?? ''}
                  onChange={(e) => setFields((s) => ({ ...s, [f.key]: e.target.value }))}
                  rows={7}
                  spellCheck={false}
                  className="num w-full resize-y rounded-lg border border-line bg-bg px-3 py-2 text-[12px] outline-none focus:border-accent"
                  placeholder='{"auth":{...},"account":{...}}'
                />
              ) : (
                <input
                  value={fields[f.key] ?? ''}
                  onChange={(e) => setFields((s) => ({ ...s, [f.key]: e.target.value }))}
                  className="h-9 w-full rounded-lg border border-line bg-bg px-3 text-[13px] outline-none focus:border-accent"
                />
              )}
            </label>
          ))}

          {phase.kind === 'waiting' && (
            <div className="rounded-xl border border-line bg-sunken/60 px-4 py-3 text-[13px]">
              <div className="flex items-center gap-2 font-medium">
                <Loader2 size={14} className="animate-spin text-accent" />
                等待在授权页完成登录…
              </div>
              <p className="mt-1 text-[12px] text-muted">授权页已在新窗口打开；若被浏览器拦截，请手动打开：</p>
              {phase.session.auth_url && (
                <a href={phase.session.auth_url} target="_blank" rel="noreferrer" className="num mt-1.5 inline-flex max-w-full items-center gap-1 truncate text-[12px] text-info hover:underline">
                  <ExternalLink size={12} className="shrink-0" />
                  <span className="truncate">{phase.session.auth_url}</span>
                </a>
              )}
            </div>
          )}

          {phase.kind === 'done' && (
            <div className={cx('rounded-xl border px-4 py-3 text-[13px]', phase.status.state === 'success' ? 'border-ok/30 bg-ok/[.06] text-ok' : 'border-bad/30 bg-bad/[.06] text-bad')}>
              {phase.status.message ?? (phase.status.state === 'success' ? '添加成功' : '添加失败')}
            </div>
          )}
        </div>
      )}
    </Dialog>
  )
}
