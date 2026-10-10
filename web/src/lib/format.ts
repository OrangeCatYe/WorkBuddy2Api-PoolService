const nf = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 })

/** 积分 / 数量：1.2万 以上用「万」。 */
export function fmtNum(n: number | undefined | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '—'
  if (Math.abs(n) >= 10000) return `${nf.format(n / 10000)}万`
  return nf.format(n)
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
