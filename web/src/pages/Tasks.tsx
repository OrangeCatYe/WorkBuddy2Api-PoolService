import { useState } from 'react'
import { CalendarClock, Clock, Loader2, Play } from 'lucide-react'
import { post } from '@/lib/api'
import { useProviderDetails } from '@/lib/queries'
import { useToast } from '@/components/overlay'
import { Badge, Button, Card, CardHeader, Empty, cx } from '@/components/ui'

export function TasksPage() {
  const details = useProviderDetails().filter((d) => d.tasks?.length)
  const toast = useToast()
  const [running, setRunning] = useState<string | null>(null)

  async function run(pid: string, tid: string, name: string) {
    setRunning(`${pid}/${tid}`)
    try {
      const r = await post<{ message?: string }>(`/api/providers/${pid}/tasks/${tid}/run`)
      toast.ok(`${name}：${r.message ?? '已开始'}`)
    } catch (e) {
      toast.bad((e as Error).message)
    } finally {
      setTimeout(() => setRunning(null), 600)
    }
  }

  if (!details.length) return <Card><Empty icon={<CalendarClock size={28} />} title="没有可用任务" hint="已启用的平台没有声明定时任务。" /></Card>

  return (
    <div className="space-y-6">
      {details.map((d) => (
        <Card key={d.meta.id}>
          <CardHeader title={d.meta.name} hint="定时规则在配置文件中调整；这里可立即手动执行一次（作用于全部账号）" />
          <ul className="grid gap-px overflow-hidden rounded-b-2xl border-t border-line bg-line sm:grid-cols-2">
            {d.tasks!.map((t) => {
              const key = `${d.meta.id}/${t.id}`
              return (
                <li key={t.id} className="flex items-start justify-between gap-4 bg-surface px-5 py-4">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className={cx('h-1.5 w-1.5 rounded-full', t.enabled ? 'bg-ok' : 'bg-faint')} />
                      <span className="font-medium">{t.name}</span>
                      {!t.enabled && <Badge>已关闭</Badge>}
                    </div>
                    {t.description && <p className="mt-1 text-[12.5px] text-muted">{t.description}</p>}
                    {t.hours?.length ? (
                      <div className="mt-2 flex flex-wrap items-center gap-1 text-[11.5px] text-faint">
                        <Clock size={11} />
                        {t.hours.map((h) => (
                          <span key={h} className="num rounded bg-sunken px-1.5 py-0.5">
                            {String(h).padStart(2, '0')}:00
                          </span>
                        ))}
                      </div>
                    ) : null}
                  </div>
                  <Button size="sm" disabled={running === key} onClick={() => run(d.meta.id, t.id, t.name)} icon={running === key ? <Loader2 size={12} className="animate-spin" /> : <Play size={12} />}>
                    执行
                  </Button>
                </li>
              )
            })}
          </ul>
        </Card>
      ))}
    </div>
  )
}
