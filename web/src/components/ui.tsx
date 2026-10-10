import { clsx } from 'clsx'
import type { ButtonHTMLAttributes, HTMLAttributes, ReactNode } from 'react'
import type { AccountStatus } from '@/types'

export const cx = clsx

// ---- Button ----
type Variant = 'primary' | 'ghost' | 'outline' | 'danger'
const variants: Record<Variant, string> = {
  primary: 'bg-ink text-bg hover:bg-ink/85 shadow-sm',
  outline: 'border border-line bg-surface hover:border-faint text-ink',
  ghost: 'text-muted hover:text-ink hover:bg-sunken',
  danger: 'bg-bad text-white hover:bg-bad/90',
}

export function Button({
  variant = 'outline',
  size = 'md',
  className,
  icon,
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: 'sm' | 'md'; icon?: ReactNode }) {
  return (
    <button
      className={cx(
        'inline-flex select-none items-center justify-center gap-1.5 rounded-lg font-medium transition-all',
        'disabled:pointer-events-none disabled:opacity-45 active:scale-[.98]',
        size === 'sm' ? 'h-7 px-2.5 text-[12.5px]' : 'h-9 px-3.5 text-[13.5px]',
        variants[variant],
        className,
      )}
      {...rest}
    >
      {icon}
      {children}
    </button>
  )
}

// ---- Card ----
export function Card({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cx('relative rounded-2xl border border-line bg-surface shadow-card', className)} {...rest} />
}

export function CardHeader({ title, hint, actions }: { title: ReactNode; hint?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 px-5 pb-3 pt-4">
      <div>
        <h3 className="font-display text-[15px] font-semibold tracking-tight">{title}</h3>
        {hint && <p className="mt-0.5 text-[12.5px] text-muted">{hint}</p>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  )
}

// ---- Badge / Status ----
type Tone = 'neutral' | 'ok' | 'warn' | 'bad' | 'info' | 'accent'
const tones: Record<Tone, string> = {
  neutral: 'bg-sunken text-muted',
  ok: 'bg-ok/12 text-ok',
  warn: 'bg-warn/14 text-warn',
  bad: 'bg-bad/12 text-bad',
  info: 'bg-info/12 text-info',
  accent: 'bg-accent/15 text-accent',
}

export function Badge({ tone = 'neutral', className, children }: { tone?: Tone; className?: string; children: ReactNode }) {
  return (
    <span className={cx('inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11.5px] font-medium', tones[tone], className)}>
      {children}
    </span>
  )
}

const statusMap: Record<AccountStatus, { tone: Tone; label: string }> = {
  active: { tone: 'ok', label: '可用' },
  cooling: { tone: 'warn', label: '冷却中' },
  paused: { tone: 'info', label: '已暂停' },
  disabled: { tone: 'bad', label: '已禁用' },
  error: { tone: 'bad', label: '异常' },
}

export function StatusBadge({ status }: { status: AccountStatus }) {
  const s = statusMap[status] ?? statusMap.error
  return (
    <Badge tone={s.tone}>
      <span className={cx('h-1.5 w-1.5 rounded-full bg-current', status === 'active' && 'animate-pulseDot')} />
      {s.label}
    </Badge>
  )
}

// ---- Meter ----
export function Meter({ value, tone = 'accent', className }: { value: number; tone?: 'accent' | 'ok' | 'warn' | 'bad'; className?: string }) {
  const color = { accent: 'bg-accent', ok: 'bg-ok', warn: 'bg-warn', bad: 'bg-bad' }[tone]
  return (
    <div className={cx('h-1.5 w-full overflow-hidden rounded-full bg-sunken', className)}>
      <div className={cx('h-full rounded-full transition-[width] duration-700', color)} style={{ width: `${Math.max(0, Math.min(100, value))}%` }} />
    </div>
  )
}

// ---- Skeleton / Empty ----
export function Skeleton({ className }: { className?: string }) {
  return <div className={cx('animate-pulse rounded-md bg-sunken', className)} />
}

export function Empty({ icon, title, hint, action }: { icon?: ReactNode; title: string; hint?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-6 py-14 text-center">
      {icon && <div className="mb-1 text-faint">{icon}</div>}
      <p className="font-display text-[15px] font-semibold">{title}</p>
      {hint && <p className="max-w-sm text-[13px] text-muted">{hint}</p>}
      {action && <div className="mt-3">{action}</div>}
    </div>
  )
}
