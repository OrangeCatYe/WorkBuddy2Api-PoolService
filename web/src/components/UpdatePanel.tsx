import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, ArrowUpCircle, CheckCircle2, Download, ExternalLink, Loader2, Plus, RefreshCw, ShieldAlert, ShieldCheck, Trash2 } from 'lucide-react'
import { api, post } from '@/lib/api'
import { useUpdateSettings, useUpdateStatus } from '@/lib/queries'
import { fmtBytes, fmtRelative } from '@/lib/format'
import { Dialog, Switch, useToast } from '@/components/overlay'
import { Badge, Button, Card, CardHeader, Meter, Skeleton, cx } from '@/components/ui'
import type { UpdateSettings, UpdateSource, UpdateStatus } from '@/types'

const RELEASES = 'https://github.com/OrangeCatYe/WorkBuddy2Api-PoolService/releases/latest'
const inputCls = 'h-8 rounded-lg border border-line bg-surface px-2.5 text-[13px] outline-none transition-colors focus:border-accent'
const INTERVALS = [
  ['6h', '每 6 小时'],
  ['12h', '每 12 小时'],
  ['24h', '每天'],
  ['72h', '每 3 天'],
  ['168h', '每周'],
] as const

/** "24h0m0s" → "24h"，用于匹配下拉选项。 */
function intervalKey(s: string): string {
  const m = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/.exec(s)
  if (!m) return s
  const mins = Number(m[1] ?? 0) * 60 + Number(m[2] ?? 0) + Number(m[3] ?? 0) / 60
  return mins % 60 === 0 ? `${mins / 60}h` : s
}

function Row({ title, hint, children }: { title: string; hint?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-x-6 gap-y-2 px-5 py-3.5 sm:flex-row sm:items-start sm:justify-between">
      <div className="min-w-0">
        <div className="text-[13.5px] font-medium">{title}</div>
        {hint && <div className="mt-0.5 text-[12.5px] text-muted">{hint}</div>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

// ---------------- 状态卡片 ----------------

function useUpdateActions() {
  const qc = useQueryClient()
  const toast = useToast()
  const set = (s: UpdateStatus) => qc.setQueryData(['update', 'status'], s)
  const fail = (e: Error) => {
    toast.bad(e.message)
    qc.invalidateQueries({ queryKey: ['update', 'status'] })
  }
  return {
    check: useMutation({ mutationFn: () => post<UpdateStatus>('/api/update/check', {}), onSuccess: set, onError: fail }),
    download: useMutation({ mutationFn: () => post<UpdateStatus>('/api/update/download'), onSuccess: set, onError: fail }),
    apply: useMutation({ mutationFn: () => post<UpdateStatus>('/api/update/apply'), onError: fail }),
  }
}

/**
 * 安装后服务会退出并重启。这里轮询状态，等到「新进程」应答（state 不再是 applying）后再等更新程序写出结果：
 * 成功就刷新页面（新版本的前端资源也随程序更新了），回滚则提示失败原因。
 */
function useRestartWatcher(active: boolean, onDone: (s: UpdateStatus | null) => void) {
  const cb = useRef(onDone)
  cb.current = onDone
  useEffect(() => {
    if (!active) return
    let stop = false
    const begin = Date.now()
    let newProcessAt = 0
    const tick = async () => {
      if (stop) return
      try {
        const s = await api<UpdateStatus>('/api/update/status')
        if (s.state !== 'applying') {
          newProcessAt ||= Date.now()
          if (s.last_result || Date.now() - newProcessAt > 90_000) return cb.current(s)
        }
      } catch {
        // 服务正在重启，连不上是预期内的，继续等。
      }
      if (Date.now() - begin > 240_000) return cb.current(null)
      setTimeout(tick, 1000)
    }
    const t = setTimeout(tick, 1500)
    return () => {
      stop = true
      clearTimeout(t)
    }
  }, [active])
}

function StatusCard() {
  const { data: st, isLoading, error } = useUpdateStatus()
  const act = useUpdateActions()
  const toast = useToast()
  const qc = useQueryClient()
  const [confirm, setConfirm] = useState(false)
  const [restarting, setRestarting] = useState(false)

  useRestartWatcher(restarting, (s) => {
    setRestarting(false)
    qc.invalidateQueries()
    if (!s) return toast.bad('等待服务重启超时，请检查服务是否已启动')
    if (s.last_result?.ok) {
      toast.ok(`${s.last_result.message}，即将刷新页面`)
      setTimeout(() => window.location.reload(), 1200)
    } else if (s.last_result) {
      toast.bad(s.last_result.message)
    } else {
      window.location.reload()
    }
  })

  if (isLoading || !st) return <Card><div className="p-5">{error ? <p className="text-[13px] text-bad">{(error as Error).message}</p> : <Skeleton className="h-24" />}</div></Card>

  const cur = st.current
  const latest = st.latest
  const busy = act.check.isPending || st.state === 'checking'
  const pct = st.progress && st.progress.total ? Math.min(100, (st.progress.done / st.progress.total) * 100) : 0

  const doApply = () => {
    setConfirm(false)
    act.apply.mutate(undefined, { onSuccess: () => setRestarting(true) })
  }

  return (
    <Card>
      <CardHeader
        title="软件更新"
        hint="从 GitHub 发布页检查新版本，下载后经签名与哈希校验，一键安装并自动重启。"
        actions={
          <Button size="sm" icon={busy ? <Loader2 size={13} className="animate-spin" /> : <RefreshCw size={13} />} disabled={busy || restarting || st.state === 'downloading' || st.state === 'applying'} onClick={() => act.check.mutate()}>
            检查更新
          </Button>
        }
      />
      <div className="divide-y divide-line border-t border-line">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-4">
          <div>
            <div className="eyebrow">当前版本</div>
            <div className="num mt-0.5 text-[18px] font-semibold">{cur.version}</div>
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            {cur.release ? <Badge tone="ok">正式版</Badge> : <Badge tone="warn">开发版</Badge>}
            <Badge>{cur.platform}</Badge>
            {cur.mode === 'supervised' && <Badge tone="info">由服务管理器托管</Badge>}
          </div>
          <div className="ml-auto text-[12.5px] text-faint">{st.checked_at ? `上次检查 ${fmtRelative(st.checked_at)}` : '尚未检查'}</div>
        </div>

        {!cur.can_apply && cur.reason && (
          <div className="flex items-start gap-2.5 bg-warn/[.07] px-5 py-3 text-[12.5px]">
            <AlertTriangle size={15} className="mt-0.5 shrink-0 text-warn" />
            <span className="text-muted">{cur.reason}</span>
          </div>
        )}

        {st.last_result && !restarting && (
          <div className={cx('flex items-start gap-2.5 px-5 py-3 text-[12.5px]', st.last_result.ok ? 'bg-ok/[.07]' : 'bg-bad/[.07]')}>
            {st.last_result.ok ? <CheckCircle2 size={15} className="mt-0.5 shrink-0 text-ok" /> : <ShieldAlert size={15} className="mt-0.5 shrink-0 text-bad" />}
            <span className="text-muted">
              上次更新（{st.last_result.from} → {st.last_result.to}）：{st.last_result.message}
            </span>
          </div>
        )}

        {st.state === 'error' && (
          <div className="flex items-start gap-2.5 bg-bad/[.06] px-5 py-3 text-[12.5px]">
            <AlertTriangle size={15} className="mt-0.5 shrink-0 text-bad" />
            <span className="break-words text-bad">{st.error}</span>
          </div>
        )}

        {st.state === 'up_to_date' && (
          <div className="flex items-center gap-2 px-5 py-3.5 text-[13px] text-ok">
            <CheckCircle2 size={15} /> 已是最新版本
          </div>
        )}

        {latest && st.state !== 'up_to_date' && (
          <div className="space-y-3 px-5 py-4">
            <div className="flex flex-wrap items-center gap-2">
              <ArrowUpCircle size={18} className="text-accent" />
              <span className="text-[14px] font-medium">
                发现新版本 <span className="num">{latest.tag}</span>
              </span>
              {latest.signed ? (
                <Badge tone="ok">
                  <ShieldCheck size={11} /> 签名有效
                </Badge>
              ) : (
                <Badge tone="warn">未签名</Badge>
              )}
              {!!latest.size && <span className="text-[12.5px] text-faint">{fmtBytes(latest.size)}</span>}
            </div>
            {latest.notes && <pre className="max-h-56 overflow-auto whitespace-pre-wrap rounded-lg border border-line bg-sunken/50 p-3 text-[12.5px] leading-relaxed text-muted">{latest.notes}</pre>}
            {latest.reason && <p className="text-[12.5px] text-warn">{latest.reason}</p>}

            {st.state === 'downloading' && (
              <div className="space-y-1.5">
                <Meter value={pct} />
                <div className="num text-[12px] text-faint">
                  下载中 {fmtBytes(st.progress?.done)} / {fmtBytes(st.progress?.total)}（{pct.toFixed(0)}%）
                </div>
              </div>
            )}

            <div className="flex flex-wrap items-center gap-2">
              {(st.state === 'available' || st.state === 'error') && cur.can_apply && latest.installable && (
                <Button variant="primary" icon={<Download size={14} />} disabled={act.download.isPending} onClick={() => act.download.mutate()}>
                  下载更新
                </Button>
              )}
              {st.state === 'ready' && cur.can_apply && (
                <>
                  <Button variant="primary" icon={<ArrowUpCircle size={14} />} onClick={() => setConfirm(true)}>
                    安装并重启
                  </Button>
                  <span className="text-[12.5px] text-ok">已下载并通过校验</span>
                </>
              )}
              {(!cur.can_apply || !latest.installable) && (
                <a href={RELEASES} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-[13px] text-info hover:underline">
                  <ExternalLink size={13} /> 前往发布页手动下载
                </a>
              )}
            </div>
          </div>
        )}

        {(st.state === 'applying' || restarting) && (
          <div className="flex items-center gap-2.5 bg-accent/[.06] px-5 py-3.5 text-[13px]">
            <Loader2 size={15} className="animate-spin text-accent" />
            正在更新并重启服务，请稍候…页面会在完成后自动刷新
          </div>
        )}
      </div>

      <Dialog
        open={confirm}
        onClose={() => setConfirm(false)}
        title={`安装 ${latest?.tag ?? ''} 并重启`}
        footer={
          <>
            <Button onClick={() => setConfirm(false)}>取消</Button>
            <Button variant="primary" onClick={doApply} disabled={act.apply.isPending}>
              确认安装
            </Button>
          </>
        }
      >
        <ul className="list-disc space-y-1.5 pl-5 text-[13px] text-muted">
          <li>服务会短暂停止（通常十几秒），<b className="font-medium text-ink">进行中的流式请求会被中断</b>，客户端需要重试。</li>
          <li>账号、密钥、配置和历史数据不会被改动，更新前会自动备份。</li>
          <li>新版本启动失败时会自动回滚到当前版本。</li>
          <li>建议在没有重要任务运行时进行。</li>
        </ul>
      </Dialog>
    </Card>
  )
}

// ---------------- 设置卡片 ----------------

function SourceForm({ onAdd, onCancel, pending }: { onAdd: (s: UpdateSource) => void; onCancel: () => void; pending: boolean }) {
  const [name, setName] = useState('')
  const [manifest, setManifest] = useState('')
  const [asset, setAsset] = useState('')
  const ok = name.trim() && manifest.trim().startsWith('http') && asset.includes('{name}')
  return (
    <div className="space-y-3 rounded-xl border border-line bg-sunken/40 p-4">
      <div className="grid gap-3">
        <label className="block">
          <span className="eyebrow mb-1 block">名称</span>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如：Gitee 镜像" className={cx(inputCls, 'w-full')} />
        </label>
        <label className="block">
          <span className="eyebrow mb-1 block">更新清单地址（latest.json）</span>
          <input value={manifest} onChange={(e) => setManifest(e.target.value)} placeholder="https://example.com/poolsvc/latest.json" spellCheck={false} className={cx(inputCls, 'num w-full')} />
        </label>
        <label className="block">
          <span className="eyebrow mb-1 block">安装包地址模板</span>
          <input value={asset} onChange={(e) => setAsset(e.target.value)} placeholder="https://example.com/poolsvc/{tag}/{name}" spellCheck={false} className={cx(inputCls, 'num w-full')} />
        </label>
      </div>
      <p className="text-[12px] leading-relaxed text-faint">
        模板可用 <code className="num">{'{tag}'}</code>（如 v1.2.0）、<code className="num">{'{version}'}</code>（如 1.2.0）、<code className="num">{'{name}'}</code>（安装包文件名，必须包含）。签名文件地址为清单地址后加 <code className="num">.sig</code>。
        镜像只负责搬运文件：内容仍会用内置公钥验签、用清单里的 sha256 校验，被篡改的镜像无法让你装上假程序。
      </p>
      <div className="flex justify-end gap-2">
        <Button size="sm" onClick={onCancel}>取消</Button>
        <Button
          size="sm"
          variant="primary"
          disabled={!ok || pending}
          onClick={() => onAdd({ id: 'src-' + Math.random().toString(36).slice(2, 8), name: name.trim(), manifest_url: manifest.trim(), asset_url: asset.trim() })}
        >
          添加
        </Button>
      </div>
    </div>
  )
}

function SettingsCard() {
  const { data: cfg, isLoading } = useUpdateSettings()
  const qc = useQueryClient()
  const toast = useToast()
  const [proxy, setProxy] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)

  const save = useMutation({
    mutationFn: (s: Partial<UpdateSettings>) => api<UpdateSettings>('/api/settings/update', { method: 'PUT', body: JSON.stringify({ ...cfg, ...s }) }),
    onSuccess: (s) => {
      qc.setQueryData(['update', 'settings'], s)
      setProxy(null)
      setAdding(false)
      toast.ok('已保存')
    },
    onError: (e: Error) => {
      toast.bad(e.message)
      qc.invalidateQueries({ queryKey: ['update', 'settings'] })
    },
  })

  if (isLoading || !cfg) return <Card><div className="p-5"><Skeleton className="h-32" /></div></Card>
  const custom = cfg.sources.filter((s) => !s.builtin)
  const proxyVal = proxy ?? cfg.proxy
  const iv = intervalKey(cfg.check_interval)

  return (
    <Card>
      <CardHeader title="下载来源与自动检查" hint="网络访问 GitHub 不稳定时，可切换下载源或配置代理" />
      <div className="divide-y divide-line border-t border-line">
        <Row title="下载源" hint={cfg.sources.find((s) => s.id === cfg.source)?.builtin ? '官方 GitHub 发布页' : '自定义下载源'}>
          <select value={cfg.source} disabled={save.isPending} onChange={(e) => save.mutate({ source: e.target.value })} className={cx(inputCls, 'w-full sm:w-56')}>
            {cfg.sources.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </Row>
        <Row title="代理" hint="支持 http、https、socks5。留空则跟随系统环境变量（HTTP_PROXY 等）。">
          <div className="flex items-center gap-1.5">
            <input
              value={proxyVal}
              onChange={(e) => setProxy(e.target.value)}
              placeholder="http://127.0.0.1:7890"
              spellCheck={false}
              className={cx(inputCls, 'num w-full sm:w-56')}
            />
            <Button size="sm" disabled={proxy === null || save.isPending} onClick={() => save.mutate({ proxy: proxy ?? '' })}>
              保存
            </Button>
          </div>
        </Row>
        <Row title="自动检查更新" hint="只检查，不会自动下载或安装。默认关闭；开启后会定期访问下载源。">
          <div className="flex items-center gap-3">
            {cfg.auto_check && (
              <select value={iv} disabled={save.isPending} onChange={(e) => save.mutate({ check_interval: e.target.value })} className={inputCls}>
                {!INTERVALS.some(([v]) => v === iv) && <option value={iv}>{iv}</option>}
                {INTERVALS.map(([v, l]) => (
                  <option key={v} value={v}>
                    {l}
                  </option>
                ))}
              </select>
            )}
            <Switch checked={cfg.auto_check} disabled={save.isPending} onChange={(v) => save.mutate({ auto_check: v })} />
          </div>
        </Row>
        <div className="space-y-3 px-5 py-3.5">
          <div className="flex items-center justify-between gap-3">
            <div>
              <div className="text-[13.5px] font-medium">自定义下载源</div>
              <div className="mt-0.5 text-[12.5px] text-muted">镜像、Gitee 或自建地址；内置的官方源不可删除。</div>
            </div>
            {!adding && (
              <Button size="sm" icon={<Plus size={13} />} onClick={() => setAdding(true)}>
                添加
              </Button>
            )}
          </div>
          {custom.length > 0 && (
            <ul className="divide-y divide-line rounded-xl border border-line">
              {custom.map((s) => (
                <li key={s.id} className="flex items-center justify-between gap-3 px-3.5 py-2.5">
                  <div className="min-w-0">
                    <div className="truncate text-[13px] font-medium">{s.name}</div>
                    <div className="num truncate text-[11.5px] text-faint">{s.manifest_url}</div>
                  </div>
                  <button
                    onClick={() => save.mutate({ sources: cfg.sources.filter((x) => x.id !== s.id), source: cfg.source === s.id ? 'github' : cfg.source })}
                    disabled={save.isPending}
                    className="rounded-md p-1.5 text-faint hover:bg-bad/10 hover:text-bad"
                    aria-label={`删除 ${s.name}`}
                  >
                    <Trash2 size={14} />
                  </button>
                </li>
              ))}
            </ul>
          )}
          {adding && <SourceForm pending={save.isPending} onCancel={() => setAdding(false)} onAdd={(s) => save.mutate({ sources: [...cfg.sources, s] })} />}
        </div>
      </div>
    </Card>
  )
}

export function UpdatePanel() {
  return (
    <div className="space-y-6">
      <StatusCard />
      <SettingsCard />
    </div>
  )
}
