import { getCreditPrecision } from './creditDisplay'

/**
 * 积分：始终显示完整数值，不再折算成「万」。
 * 上游单次消耗常只有几分到十几分，万为单位会把它们压成 0。
 * 小数位数由设置里的精度开关决定（默认整数个位）。
 *
 * 这里是纯函数：调用点遍布各处且存在条件调用（如 `x >= 1 ? fmtNum(x) : '0'`），
 * 不能用 hook。精度切换时由 AppShell 订阅并触发整棵子树重渲染。
 */
export function fmtNum(n: number | undefined | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '—'
  return formatCredit(n, getCreditPrecision())
}

/** 按指定精度格式化积分；大数加千分位便于阅读。 */
export function formatCredit(n: number | undefined | null, precision: 'int' | 'two'): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '—'
  const digits = precision === 'two' ? 2 : 0
  return new Intl.NumberFormat('zh-CN', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  }).format(n)
}

export function fmtTokens(n: number | undefined): string {
  if (!n) return '—'
  if (n >= 1_000_000) return `${+(n / 1_000_000).toFixed(n % 1_000_000 ? 1 : 0)}M`
  if (n >= 1000) return `${Math.round(n / 1000)}K`
  return String(n)
}

/** 相对时间：将来为「3 天后」，过去为「5 分钟前」。 */
export function fmtRelative(iso?: string, now = Date.now()): string {
  if (!iso) return '—'
  const diff = new Date(iso).getTime() - now
  const abs = Math.abs(diff)
  const units: [number, string][] = [
    [86400_000, '天'],
    [3600_000, '小时'],
    [60_000, '分钟'],
  ]
  for (const [ms, label] of units) {
    if (abs >= ms) {
      const v = Math.floor(abs / ms)
      return diff > 0 ? `${v} ${label}后` : `${v} ${label}前`
    }
  }
  return diff > 0 ? '即将' : '刚刚'
}

export function fmtDate(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export function pct(part: number, total: number): number {
  if (!total || total <= 0) return 0
  return Math.max(0, Math.min(100, (part / total) * 100))
}
