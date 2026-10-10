import { NavLink, Outlet, useLocation } from 'react-router-dom'
import {
  Activity,
  BookOpen,
  Boxes,
  CalendarClock,
  Coins,
  KeyRound,
  LayoutDashboard,
  Moon,
  ScrollText,
  Settings,
  Sun,
  Users,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { useMeta, useOverview } from '@/lib/queries'
import { useTheme } from '@/lib/theme'
import { cx } from './ui'

type NavItem = { to: string; label: string; icon: ReactNode; end?: boolean }
const groups: { title: string; items: NavItem[] }[] = [
  { title: '总览', items: [{ to: '/', label: '概览', icon: <LayoutDashboard size={16} />, end: true }] },
  {
    title: '资源',
    items: [
      { to: '/accounts', label: '账号', icon: <Users size={16} /> },
      { to: '/credits', label: '积分中心', icon: <Coins size={16} /> },
    ],
  },
  {
    title: '网关',
    items: [
      { to: '/gateway', label: '接入与密钥', icon: <KeyRound size={16} /> },
      { to: '/models', label: '模型目录', icon: <Boxes size={16} /> },
    ],
  },
  {
    title: '观测',
    items: [
      { to: '/usage', label: '用量分析', icon: <Activity size={16} /> },
      { to: '/logs', label: '请求日志', icon: <ScrollText size={16} /> },
    ],
  },
  { title: '自动化', items: [{ to: '/tasks', label: '任务', icon: <CalendarClock size={16} /> }] },
  { title: '系统', items: [{ to: '/settings', label: '设置', icon: <Settings size={16} /> }] },
]

const titles: Record<string, string> = Object.fromEntries(groups.flatMap((g) => g.items.map((i) => [i.to, i.label])))

function Brand() {
  return (
    <div className="flex items-center gap-2.5 px-2">
      <svg viewBox="0 0 32 32" className="h-8 w-8 shrink-0">
        <rect width="32" height="32" rx="9" className="fill-ink" />
        <circle cx="12" cy="16" r="6" fill="none" className="stroke-accent" strokeWidth="2.6" />
        <circle cx="20" cy="16" r="6" fill="none" className="stroke-bg" strokeWidth="2.6" />
      </svg>
      <div className="leading-tight">
        <div className="font-display text-[16px] font-bold tracking-tight">PoolService</div>
        <div className="eyebrow !text-[10px]">账号池 · 网关</div>
      </div>
    </div>
  )
}

function ProviderDock() {
  const { data: meta } = useMeta()
  const { data: ov } = useOverview()
  const byId = new Map(ov?.providers.map((p) => [p.id, p]))
  return (
    <div className="mx-2 rounded-xl border border-line bg-sunken/60 p-2.5">
      <div className="eyebrow mb-2 px-1">已接入平台</div>
      <ul className="space-y-1">
        {meta?.providers.map((p) => {
          const s = byId.get(p.id)
          const healthy = s ? s.active > 0 : false
          return (
            <li key={p.id}>
              <NavLink
                to={`/accounts?provider=${p.id}`}
                className="flex items-center justify-between rounded-lg px-1.5 py-1 text-[13px] hover:bg-surface"
              >
                <span className="flex items-center gap-2">
                  <span className={cx('h-1.5 w-1.5 rounded-full', healthy ? 'bg-ok' : 'bg-faint')} />
                  {p.name}
                </span>
                <span className="num text-[11.5px] text-faint">
                  {s?.active ?? 0}/{s?.total ?? 0}
                </span>
              </NavLink>
            </li>
          )
        })}
        {!meta?.providers.length && <li className="px-1.5 py-1 text-[12.5px] text-faint">尚未启用任何平台</li>}
      </ul>
    </div>
  )
}

export function AppShell() {
  const { theme, toggle } = useTheme()
  const { data: meta } = useMeta()
  const loc = useLocation()
  const title = titles[loc.pathname] ?? ''

  return (
    <div className="relative z-10 flex h-full">
      <aside className="flex w-[232px] shrink-0 flex-col gap-5 border-r border-line bg-surface/70 py-5 backdrop-blur">
        <Brand />
        <nav className="flex-1 space-y-4 overflow-y-auto px-2">
          {groups.map((g) => (
            <div key={g.title}>
              <div className="eyebrow mb-1 px-2.5">{g.title}</div>
              {g.items.map((it) => (
                <NavLink
                  key={it.to}
                  to={it.to}
                  end={it.end}
                  className={({ isActive }) =>
                    cx(
                      'group relative flex items-center gap-2.5 rounded-lg px-2.5 py-[7px] text-[13.5px] transition-colors',
                      isActive ? 'bg-sunken font-medium text-ink' : 'text-muted hover:bg-sunken/60 hover:text-ink',
                    )
                  }
                >
                  {({ isActive }) => (
                    <>
                      <span className={cx('absolute left-0 top-1/2 h-4 w-[3px] -translate-y-1/2 rounded-r bg-accent transition-opacity', isActive ? 'opacity-100' : 'opacity-0')} />
                      <span className={isActive ? 'text-accent' : ''}>{it.icon}</span>
                      {it.label}
                    </>
                  )}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
        <ProviderDock />
        <div className="flex items-center justify-between px-4 text-[12px] text-faint">
          <span className="num flex items-center gap-1">
            <BookOpen size={13} /> v{meta?.version ?? '—'}
          </span>
          <button onClick={toggle} className="rounded-md p-1.5 hover:bg-sunken hover:text-ink" aria-label="切换主题">
            {theme === 'dark' ? <Sun size={15} /> : <Moon size={15} />}
          </button>
        </div>
      </aside>

      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center justify-between border-b border-line px-8">
          <h1 className="font-display text-[19px] font-semibold tracking-tight">{title}</h1>
          <span className="eyebrow">{new Date().toLocaleDateString('zh-CN', { month: 'long', day: 'numeric', weekday: 'short' })}</span>
        </header>
        <div className="flex-1 overflow-y-auto">
          <div key={loc.pathname} className="mx-auto max-w-[1280px] animate-rise px-8 py-7">
            <Outlet />
          </div>
        </div>
      </main>
    </div>
  )
}
