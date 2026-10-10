import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useGatewaySettings, useModels } from '@/lib/queries'
import { Segmented, Switch, useToast } from '@/components/overlay'
import { Card, CardHeader, Skeleton } from '@/components/ui'
import type { GatewaySettings } from '@/types'

function SettingRow({ title, hint, control }: { title: string; hint: React.ReactNode; control: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-6 px-5 py-4">
      <div className="min-w-0">
        <div className="text-[13.5px] font-medium">{title}</div>
        <div className="mt-0.5 text-[12.5px] text-muted">{hint}</div>
      </div>
      <div className="shrink-0 pt-0.5">{control}</div>
    </div>
  )
}

export function SettingsPage() {
  const { data, isLoading } = useGatewaySettings()
  const { data: models } = useModels()
  const qc = useQueryClient()
  const toast = useToast()
  const m = useMutation({
    mutationFn: (s: GatewaySettings) => api<GatewaySettings>('/api/settings/gateway', { method: 'PUT', body: JSON.stringify(s) }),
    onMutate: (s) => qc.setQueryData(['settings', 'gateway'], s),
    onSuccess: (s) => {
      qc.setQueryData(['settings', 'gateway'], s)
      qc.invalidateQueries({ queryKey: ['meta'] })
      toast.ok('已保存，立即生效')
    },
    onError: (e: Error) => {
      toast.bad(e.message)
      qc.invalidateQueries({ queryKey: ['settings', 'gateway'] })
    },
  })

  const sample = models?.models.find((x) => x.credits) ?? models?.models[0]
  const preview = (() => {
    if (!data || !sample) return null
    let id = data.model_naming === 'full' ? `${sample.provider}/${sample.id}` : sample.id
    if (data.credit_in_model_name && sample.credits) id += `(${sample.credits})`
    return id
  })()

  return (
    <div className="max-w-3xl space-y-6">
      <Card>
        <CardHeader title="模型命名" hint="影响 /v1/models 输出，Agent（如 ttswitch）重新拉取模型列表后生效" />
        {isLoading || !data ? (
          <div className="px-5 pb-5">
            <Skeleton className="h-16" />
          </div>
        ) : (
          <div className="divide-y divide-line border-t border-line">
            <SettingRow
              title="命名风格"
              hint="短名最简洁；多平台同名模型时，非首选平台会自动使用全名"
              control={
                <Segmented
                  value={data.model_naming}
                  onChange={(v) => m.mutate({ ...data, model_naming: v })}
                  options={[
                    { value: 'short', label: '短名' },
                    { value: 'full', label: '平台/模型' },
                    { value: 'both', label: '两者' },
                  ]}
                />
              }
            />
            <SettingRow
              title="模型名显示积分倍率"
              hint={
                <>
                  在模型 ID 与显示名末尾附加倍率，Agent 的模型选择器里直接可见。带不带后缀都能请求，倍率随优惠变化后旧名仍可用。
                </>
              }
              control={<Switch checked={data.credit_in_model_name} onChange={(v) => m.mutate({ ...data, credit_in_model_name: v })} />}
            />
            {preview && (
              <div className="flex items-center gap-3 bg-sunken/50 px-5 py-3 text-[12.5px]">
                <span className="eyebrow">预览</span>
                <code className="num rounded-md bg-surface px-2 py-1 text-ink">{preview}</code>
              </div>
            )}
          </div>
        )}
      </Card>
    </div>
  )
}
