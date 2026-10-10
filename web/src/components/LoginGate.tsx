import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { KeyRound } from 'lucide-react'
import { adminKey, api, ApiError, onUnauthorized } from '@/lib/api'
import { Button, Card } from './ui'

/** 管理密钥门禁：任意接口返回 401 时切换到输入界面。 */
export function LoginGate({ children }: { children: ReactNode }) {
  const [locked, setLocked] = useState(false)
  const [value, setValue] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const qc = useQueryClient()

  useEffect(() => onUnauthorized(() => setLocked(true)), [])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    adminKey.set(value.trim())
    try {
      await api('/api/meta')
      setLocked(false)
      qc.invalidateQueries()
    } catch (err) {
      adminKey.clear()
      setError(err instanceof ApiError && err.status === 401 ? '密钥不正确' : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (!locked) return <>{children}</>
  return (
    <div className="relative z-10 grid h-full place-items-center px-6">
      <Card className="w-full max-w-[380px] animate-rise p-7">
        <div className="mb-5 grid h-11 w-11 place-items-center rounded-xl bg-accent/15 text-accent">
          <KeyRound size={20} />
        </div>
        <h2 className="font-display text-[20px] font-semibold tracking-tight">输入管理密钥</h2>
        <p className="mt-1 text-[13px] text-muted">
          密钥见 <code className="num rounded bg-sunken px-1">config.json</code> 的 <code className="num rounded bg-sunken px-1">admin_key</code>，首次启动时也会打印在日志里。
        </p>
        <form onSubmit={submit} className="mt-5 space-y-3">
          <input
            autoFocus
            type="password"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="adm-…"
            className="num h-10 w-full rounded-lg border border-line bg-bg px-3 text-[13px] outline-none focus:border-accent"
          />
          {error && <p className="text-[12.5px] text-bad">{error}</p>}
          <Button type="submit" variant="primary" className="w-full" disabled={!value || busy}>
            {busy ? '验证中…' : '进入面板'}
          </Button>
        </form>
      </Card>
    </div>
  )
}
