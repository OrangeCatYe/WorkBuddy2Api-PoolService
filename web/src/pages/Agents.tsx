import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, Check, Plug, RotateCcw, ShieldAlert } from 'lucide-react'
import { useAgents, useKeys } from '@/lib/queries'
import type { AgentInfo } from '@/types'
import { AgentImportDialog, AgentRemoveDialog, PathText, type ImportContext } from '@/components/AgentImportDialog'
import { Badge, Button, Card, CardHeader, Empty, Skeleton, cx } from '@/components/ui'

const protocolLabel: Record<AgentInfo['protocol'], string> = {
  chat: 'OpenAI Chat',
  anthropic: 'Anthropic Messages',
  responses: 'OpenAI Responses',
}

const selectCls = 'h-9 w-full rounded-lg border border-line bg-bg px-2.5 text-[13px] outline-none focus:border-accent'

function AgentCard({ a, onImport, onRemove }: { a: AgentInfo; onImport: () => void; onRemove: () => void }) {
  const first = a.files[0]
  return (
    <Card className="flex flex-col p-4">
      <div className="flex items-start gap-3">
        <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-sunken font-display text-[16px] font-bold">{a.name[0]}</div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <h3 className="font-display text-[15px] font-semibold tracking-tight">{a.name}</h3>
            {a.imported ? (
              <Badge tone="accent">
                <Check size={11} />
                已导入{a.imported_models?.length ? ` ${a.imported_models.length} 个模型` : ''}
              </Badge>
            ) : a.installed ? (
              <Badge tone="ok">已检测到</Badge>
            ) : (
              <Badge>未检测到</Badge>
            )}
          </div>
          <div className="mt-0.5 text-[12px] text-faint">{protocolLabel[a.protocol]}</div>
        </div>
      </div>

      <div className="mt-3 min-h-[40px] space-y-1">
        {first && <PathText path={first.path} className="text-[11.5px]" />}
        {a.takeover && (
          <div className="flex items-center gap-1 text-[12px] text-warn">
            <ShieldAlert size={12} className="shrink-0" />
            会替换它当前使用的接口，可随时移除还原
          </div>
        )}
        {a.blocked && (
          <div className="flex items-start gap-1 text-[12px] text-bad">
            <AlertTriangle size={12} className="mt-0.5 shrink-0" />
            <span className="line-clamp-3">{a.blocked}</span>
          </div>
        )}
      </div>

      <div className="mt-3 flex items-center gap-2">
        <Button variant={a.imported ? 'outline' : 'primary'} size="sm" onClick={onImport} disabled={!!a.blocked && !a.imported}>
          {a.imported ? '更新' : '导入'}
        </Button>
        {a.imported && (
          <Button variant="ghost" size="sm" icon={<RotateCcw size={12} />} onClick={onRemove}>
            移除
          </Button>
        )}
      </div>
    </Card>
  )
}

export function AgentsPage() {
  const { data: keys } = useKeys()
  const usable = useMemo(() => (keys ?? []).filter((k) => k.enabled && !(k.expires_at && new Date(k.expires_at).getTime() < Date.now())), [keys])
  const [keyId, setKeyId] = useState('')
  useEffect(() => {
    if (!keys) return
    if (!usable.some((k) => k.id === keyId)) setKeyId(usable[0]?.id ?? '')
  }, [keys, usable, keyId])

  const { data, isLoading, error } = useAgents(keyId)
  const [baseUrl, setBaseUrl] = useState('')
  const [importing, setImporting] = useState<AgentInfo | null>(null)
  const [removing, setRemoving] = useState<AgentInfo | null>(null)

  const key = usable.find((k) => k.id === keyId)
  const ctx: ImportContext = {
    keyId,
    keyName: key?.name ?? '',
    noKeys: !!data?.no_keys,
    baseUrl: baseUrl.trim() || data?.base_url || '',
    models: data?.models ?? [],
    canWrite: !!data?.can_write,
    writeBlock: data?.write_block ?? '',
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader title="快速导入" hint="把本网关的地址、密钥和模型一键写进其他 Agent 的配置，不用再手动改配置文件" actions={<Plug size={18} className="text-faint" />} />
        <div className="grid gap-4 border-t border-line px-5 py-4 md:grid-cols-2">
          <label className="block">
            <span className="text-[12.5px] font-medium">使用的密钥</span>
            {usable.length ? (
              <select value={keyId} onChange={(e) => setKeyId(e.target.value)} className={cx(selectCls, 'mt-1')}>
                {usable.map((k) => (
                  <option key={k.id} value={k.id}>
                    {k.name}
                    {k.models?.length ? `（限 ${k.models.length} 条模型规则）` : ''}
                  </option>
                ))}
              </select>
            ) : (
              <div className="mt-1 rounded-lg border border-line bg-sunken/50 px-3 py-2 text-[12.5px] text-muted">
                {data?.no_keys ? (
                  <>还没有任何密钥，导入时会自动创建一把「默认密钥（全部模型）」。也可以先到 <Link to="/gateway" className="text-accent underline">接入与密钥</Link> 自己创建。</>
                ) : (
                  <>没有可用的密钥（都已停用或过期），请到 <Link to="/gateway" className="text-accent underline">接入与密钥</Link> 处理。</>
                )}
              </div>
            )}
            <span className="mt-1 block text-[11.5px] text-faint">所选密钥有模型白名单时，只能导入白名单内的模型</span>
          </label>
          <label className="block">
            <span className="text-[12.5px] font-medium">网关地址</span>
            <input
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder={data?.base_url ?? ''}
              className="num mt-1 h-9 w-full rounded-lg border border-line bg-bg px-3 text-[13px] outline-none focus:border-accent"
            />
            <span className="mt-1 block text-[11.5px] text-faint">Agent 与本服务不在同一台电脑时，改成对方能访问到的地址</span>
          </label>
        </div>
        {data && !data.can_write && (
          <div className="mx-5 mb-4 flex items-start gap-2.5 rounded-xl border border-warn/30 bg-warn/[.07] px-3.5 py-2.5 text-[12.5px]">
            <ShieldAlert size={15} className="mt-0.5 shrink-0 text-warn" />
            <p className="leading-relaxed text-muted">
              <b className="font-medium text-ink">当前不能直接写入本机文件。</b>
              {data.write_block}。仍然可以预览，并复制完整配置手动粘贴。
            </p>
          </div>
        )}
      </Card>

      {isLoading && (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-[150px] rounded-2xl" />
          ))}
        </div>
      )}
      {error && <Empty title="加载失败" hint={(error as Error).message} />}
      {data && (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {data.agents.map((a) => (
            <AgentCard key={a.id} a={a} onImport={() => setImporting(a)} onRemove={() => setRemoving(a)} />
          ))}
        </div>
      )}

      <AgentImportDialog agent={importing} ctx={ctx} onClose={() => setImporting(null)} />
      <AgentRemoveDialog agent={removing} onClose={() => setRemoving(null)} />
    </div>
  )
}
