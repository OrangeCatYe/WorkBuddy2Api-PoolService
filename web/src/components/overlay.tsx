import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { CheckCircle2, X, XCircle } from 'lucide-react'
import { cx } from './ui'

function useEsc(open: boolean, onClose: () => void) {
  useEffect(() => {
    if (!open) return
    const h = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [open, onClose])
}

export function Dialog({ open, onClose, title, children, footer, width = 480 }: { open: boolean; onClose: () => void; title: ReactNode; children: ReactNode; footer?: ReactNode; width?: number }) {
  useEsc(open, onClose)
  if (!open) return null
  return createPortal(
    <div className="fixed inset-0 z-50 grid place-items-center p-6">
      <div className="absolute inset-0 animate-fade bg-ink/30 backdrop-blur-[2px]" onClick={onClose} />
      <div role="dialog" className="relative w-full animate-rise rounded-2xl border border-line bg-surface shadow-pop" style={{ maxWidth: width }}>
        <div className="flex items-center justify-between px-5 pb-2 pt-4">
          <h3 className="font-display text-[16px] font-semibold tracking-tight">{title}</h3>
          <button onClick={onClose} className="rounded-md p-1 text-faint hover:bg-sunken hover:text-ink" aria-label="关闭">
            <X size={16} />
          </button>
        </div>
        <div className="px-5 pb-5">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t border-line px-5 py-3">{footer}</div>}
      </div>
    </div>,
    document.body,
  )
}

export function Drawer({ open, onClose, title, subtitle, children }: { open: boolean; onClose: () => void; title: ReactNode; subtitle?: ReactNode; children: ReactNode }) {
  useEsc(open, onClose)
  if (!open) return null
  return createPortal(
    <div className="fixed inset-0 z-40">
      <div className="absolute inset-0 animate-fade bg-ink/20" onClick={onClose} />
      <aside className="absolute right-0 top-0 flex h-full w-full max-w-[480px] animate-slideIn flex-col border-l border-line bg-surface shadow-pop">
        <div className="flex items-start justify-between gap-3 border-b border-line px-6 py-4">
          <div className="min-w-0">
            <h3 className="truncate font-display text-[17px] font-semibold tracking-tight">{title}</h3>
            {subtitle && <div className="mt-0.5 text-[12px] text-faint">{subtitle}</div>}
          </div>
          <button onClick={onClose} className="rounded-md p-1 text-faint hover:bg-sunken hover:text-ink" aria-label="关闭">
            <X size={16} />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto px-6 py-5">{children}</div>
      </aside>
    </div>,
    document.body,
  )
}

export function Switch({ checked, onChange, disabled }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <button
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cx('relative h-[22px] w-[38px] shrink-0 rounded-full transition-colors disabled:opacity-50', checked ? 'bg-accent' : 'bg-line')}
    >
      <span className={cx('absolute top-[3px] h-4 w-4 rounded-full bg-white shadow transition-[left]', checked ? 'left-[19px]' : 'left-[3px]')} />
    </button>
  )
}

export function Segmented<T extends string>({ value, onChange, options }: { value: T; onChange: (v: T) => void; options: { value: T; label: string }[] }) {
  return (
    <div className="inline-flex rounded-lg border border-line bg-sunken/60 p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          onClick={() => onChange(o.value)}
          className={cx('rounded-md px-3 py-1 text-[12.5px] transition-colors', value === o.value ? 'bg-surface font-medium text-ink shadow-sm' : 'text-muted hover:text-ink')}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

// ---- Toast ----
type Toast = { id: number; tone: 'ok' | 'bad'; text: string }
const ToastCtx = createContext<(tone: Toast['tone'], text: string) => void>(() => {})

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Toast[]>([])
  const seq = useRef(0)
  const push = useCallback((tone: Toast['tone'], text: string) => {
    const id = ++seq.current
    setItems((s) => [...s.slice(-3), { id, tone, text }])
    setTimeout(() => setItems((s) => s.filter((t) => t.id !== id)), tone === 'bad' ? 6000 : 3200)
  }, [])
  return (
    <ToastCtx.Provider value={push}>
      {children}
      {createPortal(
        <div className="pointer-events-none fixed bottom-5 right-5 z-[60] flex w-[340px] flex-col gap-2">
          {items.map((t) => (
            <div key={t.id} className="pointer-events-auto flex animate-rise items-start gap-2.5 rounded-xl border border-line bg-surface px-4 py-3 text-[13px] shadow-pop">
              {t.tone === 'ok' ? <CheckCircle2 size={16} className="mt-px shrink-0 text-ok" /> : <XCircle size={16} className="mt-px shrink-0 text-bad" />}
              <span className="break-words">{t.text}</span>
            </div>
          ))}
        </div>,
        document.body,
      )}
    </ToastCtx.Provider>
  )
}

export function useToast() {
  const push = useContext(ToastCtx)
  return { ok: (t: string) => push('ok', t), bad: (t: string) => push('bad', t) }
}
