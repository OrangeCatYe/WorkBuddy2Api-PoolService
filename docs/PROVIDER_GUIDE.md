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
| `TaskCapable` | 否 | 任务页的任务卡片 |
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

## 5. 注册

在 `internal/app/app.go` 中解析 `providers.<id>` 配置段并 `reg.Register(...)`。注册顺序就是优先级：多个平台提供同名模型时，短名指向先注册的平台，其余平台的同名模型以 `平台/模型` 全名出现。

## 6. 参考实现

- `internal/providers/mock`：最小实现，只有 `Provider` + `ChatCapable`
- `internal/providers/workbuddy`：完整实现，全部能力
