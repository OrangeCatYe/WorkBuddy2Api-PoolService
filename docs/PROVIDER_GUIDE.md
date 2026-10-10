# 新平台接入指南

PoolService 用 Provider 适配器对接各类 AI 编程助手。不同平台的登录方式、额度体系、上游协议差异很大，所以框架只规定**能力接口**，不规定实现方式。

## 1. 能力清单

| 接口 | 必需 | 面板效果 |
|---|---|---|
| `Provider`（`Meta` + `Accounts`） | 是 | 侧边栏平台坞、账号表、概览汇总 |
| `ChatCapable`（`Models` + `ServeChat`） | 提供 API 时 | 模型目录、`/v1/*` 网关路由 |
| `QuotaCapable` | 否 | 账号「刷新余额」、积分中心 |
| `LoginCapable` | 否 | 「添加账号」对话框里的登录方式 |
| `AccountOps` | 否 | 账号详情与批量操作按钮 |
| `TaskCapable` | 否 | 任务页的任务卡片；账号页批量栏的「执行任务」菜单 |
| `SettingsCapable` | 否 | 设置页的平台参数标签页 |
| `QuestCapable` | 否 | 账号详情的「成长任务」页签（查看进度、自动完成、领奖） |
| `TaskHistoryAware` | 否 | 注入执行记录器，任务页显示每次执行的逐账号结果 |
| `RoutingTunable` | 否 | 积分中心的选号策略对该平台生效 |
| `Starter` | 否 | 启动 / 停止后台协程（刷新、持久化） |

定义见 `internal/core/provider/provider.go`。未实现的能力，面板自动隐藏对应入口。

## 2. 登录方式

`LoginMethod` 描述一种添加账号的方式，面板据此渲染：

- **跳转授权**（OAuth / 设备码）：`StartLogin` 返回带 `AuthURL` 的 `LoginSession`，面板打开新窗口并每 3 秒调用 `PollLogin`，直到 `success / failed / expired`。
- **表单提交**（API Key、Cookie、凭证 JSON）：在 `Fields` 声明字段（`kind: text / textarea`），`StartLogin` 直接返回 `LoginStatus`。

一个平台可以同时提供多种方式，例如 WorkBuddy 提供「国内版授权 / 国际版授权 / 导入凭证」。

## 3. 账号模型

`Accounts()` 返回统一的 `account.Account`：

- `Status` 只有五种：`active / cooling / paused / disabled / error`，平台内部更细的状态写进 `StatusReason` 与 `Meta`。
- `Quota.Packages` 用于积分中心的到期分析，平台有分批到期的额度时尽量填写；周期额度填 `CycleResetAt`。
- 平台特有字段放进 `Meta`，并在 `Meta().AccountFields` 声明要在详情里展示的键。

## 4. 聊天

`ServeChat` 收到的是网关规范化后的请求：

- `Model` 已去掉平台前缀、别名与积分倍率后缀
- `reasoning_effort` 已按 `Model.ReasoningLevels` 映射到模型支持的档位
- 网关鉴权已完成，`Header` 里不含 `Authorization`

Provider 直接向 `w` 写出 OpenAI Chat Completions 格式（SSE 或 JSON）。网关在外层负责 `model` 回显、`usage` 采集和日志，Provider 不需要处理。

## 5. 任务

`RunTask(ctx, taskID, accountIDs)`：`accountIDs` 为空表示全部账号，非空时只处理这些账号（面板在账号页勾选后从批量栏触发）。指定账号时建议同时处理已禁用的账号，因为是用户明确选中的。

### 执行记录

实现 `TaskHistoryAware` 后，核心层会注入 `*taskrun.Recorder`。每次执行（定时或手动）调用 `Begin` 开一条记录，逐账号 `Add` 结果（状态 `ok / skipped / error`、说明、奖励），最后 `End`。记录先在内存里实时更新，结束后按天写入 `data/task-runs/`，保留 30 天。同一任务有执行中的记录时，`RunTask` 应拒绝再次触发。

### 账号级任务

`QuestCapable` 用于平台自带的任务体系（如 WorkBuddy 成长任务）：`Quests` 列出任务与进度，`StartQuests` 异步自动完成并返回执行记录 ID（面板据此轮询），`ClaimQuest` 领奖，`AcceptQuests` 批量接受。

## 6. 平台参数

实现 `SettingsCapable` 后，面板会为该平台生成一个设置标签页，不需要写前端代码：

- `SettingsSchema()` 声明参数。`Key` 是 `providers.<id>` 配置段内的点分路径，`Kind` 决定控件：`bool` 开关、`int / float` 数字框（带 `Min / Max / Unit`）、`duration` 时长（如 `30m`）、`hours` 24 格整点选择、`select` 分段或下拉、`text`、`secret`（不回显）。`Group` 分组，`EnabledBy` 指定依赖的开关，`Restart` 标记需要重启才生效。
- `SettingsRaw()` 返回已保存的完整配置段（含默认值）。
- `ApplySettings(raw)` 校验整段新配置，校验失败时不得修改任何运行状态；通过后热应用能即时生效的字段。
- `PendingRestart()` 返回已保存但需重启才生效的 Key，面板据此显示提醒。

面板只提交改过的字段。核心层按 schema 校验类型和范围（schema 外的 Key 一律拒绝），合并进配置段后交给 `ApplySettings`，成功才写回 `config.json`。

## 7. 注册

在 `internal/app/app.go` 中解析 `providers.<id>` 配置段并 `reg.Register(...)`。注册顺序就是优先级：多个平台提供同名模型时，短名指向先注册的平台，其余平台的同名模型以 `平台/模型` 全名出现。

## 8. 参考实现

- `internal/gateway/fakeprovider_test.go`：最小实现，只有 `Provider` + `ChatCapable`（网关测试用的替身，不编进程序）
- `internal/providers/workbuddy`：完整实现，全部能力
