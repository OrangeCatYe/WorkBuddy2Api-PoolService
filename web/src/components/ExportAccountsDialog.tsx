import { useEffect, useMemo, useState } from 'react'
import { Download, Loader2, Search, ShieldAlert, X } from 'lucide-react'
import { post } from '@/lib/api'
import { useMeta } from '@/lib/queries'
import { fmtNum } from '@/lib/format'
import type { Account, ExportResult } from '@/types'
import { Dialog, useToast } from './overlay'
import { Badge, Button, StatusBadge, cx } from './ui'

const checkbox = 'h-4 w-4 shrink-0 accent-[rgb(var(--accent))]'

const pad = (n: number) => String(n).padStart(2, '0')
function stamp(d = new Date()) {
  return `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}`
}

/** 把文本作为文件交给浏览器下载。 */
function download(text: string, filename: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/**
 * 导出账号配置：勾选要导出的账号，保存为 JSON 文件。
 * 文件内容与「添加账号 → 导入凭证」接收的格式一致，可直接导入回去。
 * 平台是否支持导出由后端能力声明（capabilities 含 export）决定。
 *
 * preselected：预选的账号，形如 `平台ID/账号ID`；为空时默认勾选当前平台全部账号。
 */
export function ExportAccountsDialog({ open, onClose, accounts, preselected }: { open: boolean; onClose: () => void; accounts: Account[]; preselected: string[] }) {
  const { data: meta } = useMeta()
  const toast = useToast()
  const providers = useMemo(
    () => (meta?.providers ?? []).filter((p) => p.capabilities.includes('export') && accounts.some((a) => a.provider === p.id)),
    [meta, accounts],
  )
  const providerKey = providers.map((p) => p.id).join(',')
  const [pid, setPid] = useState('')
  const [q, setQ] = useState('')
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [busy, setBusy] = useState(false)

  const allOf = (provider: string) => new Set(accounts.filter((a) => a.provider === provider).map((a) => a.id))

  useEffect(() => {
    if (!open) return
    const first = preselected[0]?.split('/')[0]
    const p = providers.find((x) => x.id === first)?.id ?? providers[0]?.id ?? ''
    const chosen = preselected.filter((k) => k.startsWith(`${p}/`)).map((k) => k.slice(p.length + 1))
    setPid(p)
    setQ('')
    setBusy(false)
    setSel(chosen.length ? new Set(chosen) : allOf(p))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, providerKey])

  const list = useMemo(() => accounts.filter((a) => a.provider === pid), [accounts, pid])
  const shown = useMemo(() => {
    const k = q.trim().toLowerCase()
    return k ? list.filter((a) => a.label.toLowerCase().includes(k) || a.id.toLowerCase().includes(k) || a.tags?.some((t) => t.toLowerCase().includes(k))) : list
  }, [list, q])
  const shownSelected = shown.filter((a) => sel.has(a.id)).length
  const allShown = shown.length > 0 && shownSelected === shown.length

  const toggle = (id: string) =>
    setSel((s) => {
      const n = new Set(s)
      n.has(id) ? n.delete(id) : n.add(id)
      return n
    })
  const toggleShown = () =>
    setSel((s) => {
      const n = new Set(s)
      shown.forEach((a) => (allShown ? n.delete(a.id) : n.add(a.id)))
      return n
    })

  async function run() {
    if (!pid || !sel.size) return
    setBusy(true)
    try {
      const ids = list.filter((a) => sel.has(a.id)).map((a) => a.id) // 保持列表顺序
      const res = await post<ExportResult>(`/api/providers/${encodeURIComponent(pid)}/export`, { account_ids: ids })
      download(JSON.stringify(res.accounts, null, 2), `poolservice-${pid}-accounts-${stamp()}.json`)
      toast.ok(`已导出 ${res.count} 个账号`)
      if (res.missing?.length) toast.bad(`${res.missing.length} 个账号未能导出（不存在或凭证不完整）`)
      onClose()
    } catch (e) {
      toast.bad((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="导出账号配置"
      width={560}
      footer={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" onClick={run} disabled={!sel.size || busy || !pid} icon={busy ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}>
            {sel.size ? `导出 ${sel.size} 个账号` : '请选择账号'}
          </Button>
        </>
      }
    >
      <div className="space-y-3.5">
        <div className="flex items-start gap-2.5 rounded-xl border border-warn/30 bg-warn/[.07] px-3.5 py-2.5 text-[12.5px]">
          <ShieldAlert size={15} className="mt-0.5 shrink-0 text-warn" />
          <p className="leading-relaxed text-muted">
            <b className="font-medium text-ink">文件包含登录令牌明文，等同于账号密码。</b>
            请只保存在可信位置，不要发给他人，也不要上传到公开仓库。导出后在「添加账号 → 导入凭证」中载入即可恢复。
          </p>
        </div>

        {!providers.length ? (
          <p className="py-8 text-center text-[13px] text-muted">当前没有可导出的账号。</p>
        ) : (
          <>
            {providers.length > 1 && (
              <div className="flex flex-wrap gap-1.5">
                {providers.map((p) => (
                  <button
                    key={p.id}
                    disabled={busy}
                    onClick={() => {
                      setPid(p.id)
                      setQ('')
                      setSel(allOf(p.id))
                    }}
                    className={cx('rounded-lg border px-3 py-1.5 text-[13px]', p.id === pid ? 'border-ink bg-ink text-bg' : 'border-line text-muted hover:text-ink')}
                  >
                    {p.name}
                  </button>
                ))}
              </div>
            )}

            <label className="flex h-9 items-center gap-2 rounded-lg border border-line bg-bg px-3 focus-within:border-accent">
              <Search size={14} className="shrink-0 text-faint" />
              <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索昵称、ID、标签" className="w-full min-w-0 bg-transparent text-[13px] outline-none" />
              {q && (
                <button onClick={() => setQ('')} className="text-faint hover:text-ink" aria-label="清空搜索">
                  <X size={13} />
                </button>
              )}
            </label>

            <div className="overflow-hidden rounded-xl border border-line">
              <div className="flex items-center gap-2.5 border-b border-line bg-sunken/50 px-3.5 py-2">
                <input
                  type="checkbox"
                  className={checkbox}
                  checked={allShown}
                  ref={(el) => {
                    if (el) el.indeterminate = !allShown && shownSelected > 0
                  }}
                  onChange={toggleShown}
                  disabled={!shown.length}
                  aria-label="全选当前列表"
                />
                <span className="text-[12.5px] text-muted">
                  {q.trim() ? `筛选出 ${shown.length} 个` : `共 ${list.length} 个`} · 已选 <b className="num font-semibold text-ink">{sel.size}</b>
                </span>
                <button onClick={() => setSel(new Set())} disabled={!sel.size} className="ml-auto text-[12px] text-faint hover:text-ink disabled:opacity-40">
                  清空
                </button>
              </div>
              <ul className="max-h-[300px] divide-y divide-line overflow-y-auto">
                {shown.map((a) => (
                  <li key={a.id}>
                    <label className="flex cursor-pointer items-center gap-3 px-3.5 py-2.5 hover:bg-sunken/40">
                      <input type="checkbox" className={checkbox} checked={sel.has(a.id)} onChange={() => toggle(a.id)} />
                      <div className="min-w-0 flex-1">
                        <div className="flex min-w-0 items-center gap-1.5 text-[13.5px]">
                          <span className="truncate font-medium">{a.label || a.id}</span>
                          {a.tags?.map((t) => (
                            <Badge key={t} className="!px-1 !py-0 text-[10.5px]">
                              {t}
                            </Badge>
                          ))}
                        </div>
                        <div className="num truncate text-[11.5px] text-faint">{a.id}</div>
                      </div>
                      <StatusBadge status={a.status} />
                      <span className="num w-[60px] shrink-0 text-right text-[12.5px] text-muted">{a.quota && !a.quota.unlimited ? fmtNum(a.quota.remaining) : '—'}</span>
                    </label>
                  </li>
                ))}
                {!shown.length && <li className="px-4 py-8 text-center text-[13px] text-muted">没有匹配的账号</li>}
              </ul>
            </div>
          </>
        )}
      </div>
    </Dialog>
  )
}
