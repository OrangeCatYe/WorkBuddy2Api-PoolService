import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { useProviderDetail } from "@/lib/queries";
import { fmtDate, fmtNum, fmtRelative, pct } from "@/lib/format";
import type { Account, ActionDef } from "@/types";
import { Dialog, Drawer } from "./overlay";
import { Badge, Button, Meter, StatusBadge, cx } from "./ui";
import { useAccountActions } from "./useAccountActions";
import { QuestsPanel } from "./QuestsPanel";

const sourceLabel: Record<string, string> = {
  checkin: "签到",
  task: "任务",
  gift: "活动",
  trial: "试用",
  cycle: "周期",
  purchase: "购买",
  summary: "汇总",
  package: "积分包",
};

function Row({ k, v }: { k: string; v: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-1.5 text-[13px]">
      <span className="text-muted">{k}</span>
      <span className="min-w-0 truncate text-right">{v}</span>
    </div>
  );
}

export function ConfirmAction({
  pending,
  onConfirm,
  onCancel,
}: {
  pending: { action: ActionDef; ids: string[] } | null;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <Dialog
      open={!!pending}
      onClose={onCancel}
      title={`确认${pending?.action.name ?? ""}`}
      width={420}
      footer={
        <>
          <Button onClick={onCancel}>取消</Button>
          <Button variant="danger" onClick={onConfirm}>
            确认{pending?.action.name}
          </Button>
        </>
      }
    >
      <p className="text-[13.5px] text-muted">
        {pending?.action.confirm ?? "确定执行该操作？"}
        {pending && pending.ids.length > 1 && (
          <span className="mt-2 block font-medium text-ink">
            将作用于 {pending.ids.length} 个账号。
          </span>
        )}
      </p>
    </Dialog>
  );
}

export function AccountDrawer({
  account,
  onClose,
}: {
  account: Account | null;
  onClose: () => void;
}) {
  const { data: detail } = useProviderDetail(account?.provider);
  const acts = useAccountActions(account?.provider ?? "");
  const [tab, setTab] = useState<"info" | "quests">("info");
  const key = account ? `/` : "";
  useEffect(() => setTab("info"), [key]);
  if (!account) return null;
  const hasQuests = !!detail?.meta.capabilities?.includes("quests");
  const q = account.quota;
  const now = Date.now();
  const packages = [...(q?.packages ?? [])].sort(
    (a, b) =>
      (a.expire_at ? new Date(a.expire_at).getTime() : Infinity) -
      (b.expire_at ? new Date(b.expire_at).getTime() : Infinity),
  );
  const meta = account.meta ?? {};
  const limited =
    (meta.rate_limited_models as
      | { model: string; kind: string; until?: string; reason?: string }[]
      | undefined) ?? [];

  return (
    <>
      <Drawer
        open
        onClose={onClose}
        title={account.label}
        subtitle={
          <span className="num">
            {account.provider} · {account.id}
          </span>
        }
      >
        <div className="flex flex-wrap items-center gap-1.5">
          <StatusBadge status={account.status} />
          {account.tags?.map((t) => (
            <Badge key={t}>{t}</Badge>
          ))}
        </div>
        {account.status_reason && (
          <p className="mt-2 text-[12.5px] text-muted">
            {account.status_reason}
            {account.cooling_until && (
              <span className="text-faint">
                {" "}
                · {fmtRelative(account.cooling_until)}恢复
              </span>
            )}
          </p>
        )}

        {detail?.actions?.length ? (
          <div className="mt-4 flex flex-wrap gap-1.5">
            {detail.actions.map((a) => (
              <Button
                key={a.id}
                size="sm"
                variant={a.danger ? "ghost" : "outline"}
                className={
                  a.danger ? "text-bad hover:bg-bad/10 hover:text-bad" : ""
                }
                disabled={acts.busy}
                onClick={() => acts.run(a, [account.id])}
              >
                {acts.busyAction === a.id && (
                  <Loader2 size={12} className="animate-spin" />
                )}
                {a.name}
              </Button>
            ))}
          </div>
        ) : null}

        {hasQuests && (
          <div className="mt-5 flex border-b border-line">
            {(
              [
                ["info", "概况"],
                ["quests", "成长任务"],
              ] as const
            ).map(([id, label]) => (
              <button
                key={id}
                onClick={() => setTab(id)}
                className={cx(
                  "-mb-px border-b-2 px-3 py-2 text-[13px] transition-colors",
                  tab === id
                    ? "border-accent font-medium text-ink"
                    : "border-transparent text-muted hover:text-ink",
                )}
              >
                {label}
              </button>
            ))}
          </div>
        )}

        {tab === "quests" ? (
          <div className="mt-5">
            <QuestsPanel account={account} />
          </div>
        ) : (
          <>
            <section className="mt-6">
              <div className="eyebrow mb-2">积分</div>
              {q ? (
                <>
                  <div className="flex items-baseline gap-1.5">
                    <span className="num font-display text-[30px] font-semibold leading-none">
                      {fmtNum(q.remaining)}
                    </span>
                    <span className="num text-[13px] text-faint">
                      / {fmtNum(q.total)}
                    </span>
                  </div>
                  <Meter className="mt-2.5" value={pct(q.remaining, q.total)} />
                  {q.updated_at && !q.updated_at.startsWith("0001") && (
                    <p className="mt-1.5 text-[11.5px] text-faint">
                      明细更新于 {fmtRelative(q.updated_at)}
                    </p>
                  )}
                </>
              ) : (
                <p className="text-[13px] text-faint">暂无额度信息</p>
              )}
            </section>

            {packages.length > 0 && (
              <section className="mt-6">
                <div className="eyebrow mb-2">积分包 · 按到期排序</div>
                <ul className="divide-y divide-line rounded-xl border border-line">
                  {packages.map((p, i) => {
                    const expired =
                      p.expire_at && new Date(p.expire_at).getTime() <= now;
                    const soon =
                      p.expire_at &&
                      !expired &&
                      new Date(p.expire_at).getTime() - now < 48 * 3600_000;
                    return (
                      <li
                        key={i}
                        className={cx(
                          "flex items-center justify-between gap-3 px-3.5 py-2.5",
                          expired && "opacity-45",
                        )}
                      >
                        <div className="min-w-0">
                          <div className="truncate text-[13px] font-medium">
                            {p.name || "积分包"}
                          </div>
                          <div className="text-[11.5px] text-faint">
                            {sourceLabel[p.source ?? ""] ?? p.source} ·{" "}
                            {p.expire_at
                              ? `${fmtDate(p.expire_at)} 到期`
                              : "长期有效"}
                          </div>
                        </div>
                        <div className="text-right">
                          <div className="num text-[13.5px] font-semibold">
                            {fmtNum(p.remaining)}
                          </div>
                          <div
                            className={cx(
                              "text-[11.5px]",
                              soon ? "text-bad" : "text-faint",
                            )}
                          >
                            {expired
                              ? "已过期"
                              : p.expire_at
                                ? fmtRelative(p.expire_at)
                                : `/ ${fmtNum(p.amount)}`}
                          </div>
                        </div>
                      </li>
                    );
                  })}
                </ul>
              </section>
            )}

            {limited.length > 0 && (
              <section className="mt-6">
                <div className="eyebrow mb-2">模型限额</div>
                <ul className="space-y-1.5">
                  {limited.map((m) => (
                    <li
                      key={m.model}
                      className="flex items-center justify-between rounded-lg bg-warn/[.08] px-3 py-2 text-[12.5px]"
                    >
                      <span className="num">{m.model}</span>
                      <span className="text-warn">
                        {m.until ? `${fmtRelative(m.until)}恢复` : m.kind}
                      </span>
                    </li>
                  ))}
                </ul>
              </section>
            )}

            <section className="mt-6">
              <div className="eyebrow mb-1">调用</div>
              <Row
                k="成功 / 失败"
                v={
                  <span className="num">
                    {account.stats.success} / {account.stats.fail}
                  </span>
                }
              />
              <Row
                k="在途请求"
                v={<span className="num">{account.stats.in_flight}</span>}
              />
              <Row k="最近成功" v={fmtRelative(account.stats.last_used_at)} />
              {detail?.meta.account_fields?.map((f) => {
                const v = meta[f.key];
                if (v === undefined || v === "" || v === null) return null;
                return (
                  <Row
                    key={f.key}
                    k={f.label}
                    v={typeof v === "boolean" ? (v ? "是" : "否") : String(v)}
                  />
                );
              })}
            </section>
          </>
        )}
      </Drawer>
      <ConfirmAction
        pending={acts.pending}
        onConfirm={acts.confirm}
        onCancel={acts.cancel}
      />
    </>
  );
}
