# PoolService 需求梳理

> 版本：v0.1（2026-10-10）  
> 来源：`workbuddy2api-panel-main`（原项目，Go 单体 + 内嵌单页面板）、`tt-switch-master`（下游 Agent 路由工具）

## 1. 背景与目标

原项目把腾讯 CodeBuddy / WorkBuddy 账号包装成 OpenAI 兼容网关，功能完整但存在以下问题：

| 问题 | 表现 |
|---|---|
| 平台耦合 | 账号、池、调度、面板全部围绕 WorkBuddy 写死，`auth.Auth` 即账号模型，无法接入第二种软件 |
| 前端难维护 | `app.js` 148KB + `index.html` 72KB 手写单文件，无组件化、无类型 |
| 下游协议细节不对齐 | `/v1/models` 输出 `reasoning_supported_efforts`、`supports_tool_call`、`supports_images`，ttswitch 均不解析（见 §4） |
| 积分视角弱 | 积分只用于选号权重，缺少「账号 × 积分包 × 到期 × 消耗速度」的统一账本与预测 |
| 无下游身份 | 只有一个全局 `api_key`，无法区分 ttswitch / 其他客户端的用量 |

重构目标：

1. **多平台框架**：核心层与具体软件解耦，WorkBuddy 成为第一个 Provider 适配器，后续软件以插件方式接入。
2. **高质量面板**：简约、美观、交互顺畅的现代前端（组件化 + 类型安全），布局为多平台预留。
3. **ttswitch 一等公民**：作为 ttswitch 的自定义 OpenAI 兼容供应商，模型名、思考强度、能力字段、用量、错误语义全部正确。
4. **API 网关 + 积分中心**：统计各账号积分、到期时间、消耗速度，并以此驱动选号策略，最大化利用即将过期的积分。
5. **全程 git 管理**：分阶段提交，可回溯。

## 2. 原项目功能盘点（必须保留）

### 2.1 网关
- `POST /v1/chat/completions`：流式 / 非流式（上游强制 stream，本地聚合）
- `GET /v1/models`：动态拉取上游目录，带上下文、最大输出、倍率、思考档位
- `GET /status`、`GET /healthz`
- 请求改写管线：`developer` 角色归一、tool_choice 归一、image_url 兼容、DeepSeek 思维链注入、`reasoning_effort` 降级、`reasoning_content` 回填、指纹脱敏、系统提示词三模式（custom / append / passthrough）与内容拦截降级重试
- 响应：SSE 白名单重建、恰好一个 `[DONE]`、DSML 原生工具调用标记还原、错误帧附带网关提示
- CN / Global 双域（`cn:` / `global:` 模型前缀）

### 2.2 账号池
- 错误分类：402 余额耗尽、429 软限流（指数退避）、6004 模型级限流（冷却到重置时刻，切模型可用）、12153 会话失效（连续 3 次禁用）、404、5xx 熔断、11101 / 内容拦截不罚号
- 选号：成本分层、快过期 ×3 加权、积分比例 + 闲置补偿加权随机、Top-5 防惊群、在途租约、积分保底 `credit_floor`、成本探索
- 会话粘性（conversation_id / 内容哈希派生），可镜像 Upstash Redis
- 账号状态：可用 / 冷却 / 熔断 / 降权 / 禁用 / 暂停选号；企业版识别
- 状态持久化 `state.json`

### 2.3 账号运维与任务
- 浏览器内 OAuth 设备授权添加账号（热加载）、cockpit JSON 导入
- 定时：签到（+连登管家兑换与抽奖）、活跃上报、猫猫旅行、token 保活、夜猫子、余额刷新、成长任务
- 任务中心：成长任务扫描、执行队列、一键完成 17 项

### 2.4 观测
- 用量（时序、按账号 / 模型 / 域）、积分扣除历史、积分包到期分布
- 请求日志（IP / UA / 耗时 / Token / 积分）、运行日志分频道
- 模型与档位查询、输出上限探测结果
- 在线配置编辑（热生效 / 需重启区分）

## 3. 新增与变更需求

### 3.1 多平台 Provider 框架（F1）
- F1.1 定义 Provider 能力接口：元信息、登录方式、凭证存储、模型目录、聊天转发、错误分类、额度查询、定时任务、成长任务（均为可选能力，按需实现）
- F1.2 统一账号模型 `Account{id, provider, label, status, quota, tags, meta}`，原 WorkBuddy 字段放入 `meta`
- F1.3 Provider 注册表；面板按 Provider 动态渲染（登录入口、任务按钮、专属字段），未实现的能力自动隐藏
- F1.4 模型路由：`provider/model` 全名 + 无前缀短名（唯一时直达，多 Provider 同名时按优先级 / 策略）
- F1.5 第一期仅接入 WorkBuddy。不提供任何模拟 / 演示平台（`mock` 平台已于 v0.6.3 移除，网关测试改用测试文件内的替身，不编进程序）

### 3.2 面板重构（F2）
- F2.1 技术栈：React 18 + TypeScript + Vite + Tailwind CSS + TanStack Query + ECharts；构建产物 `go:embed` 进单二进制
- F2.2 信息架构（左侧导航，按域分组）：
  - 概览：健康度、今日请求 / Token / 积分消耗、即将过期积分、告警
  - 账号：按 Provider 分 Tab 的账号表 + 详情抽屉（积分包、冷却、模型锁、操作）
  - 积分中心：积分包到期日历、消耗速度、耗尽预测、浪费预警
  - 网关：下游 API Key 管理、模型目录（能力 / 档位 / 倍率）、路由与策略
  - 观测：用量分析、请求日志、运行日志
  - 自动化：定时任务、任务中心
  - 设置：服务、Provider 配置、安全
- F2.3 交互：明暗主题、全局搜索 / 命令面板（Ctrl+K）、批量操作、乐观更新与 Toast、长任务进度可视化、表格筛选排序记忆
- F2.4 响应式：桌面优先，平板可用

### 3.3 ttswitch 对接（F3，详见 §4）
- F3.1 `/v1/models` 输出 ttswitch 可解析的标准字段
- F3.2 推理强度：接受 `reasoning_effort` 五档（low / medium / high / xhigh / max）及 `reasoningEffort` 别名，按模型能力就近映射，不静默丢弃
- F3.3 用量：流式最后一帧带 `usage`（含 `prompt_tokens_details.cached_tokens`），非流式响应带完整 `usage`
- F3.4 响应 `model` 字段回显客户端请求的模型名
- F3.5 错误：OpenAI 错误结构 + 正确状态码；限流 / 无可用账号返回 429 / 503 并带 `Retry-After`
- F3.6 提供「ttswitch 接入向导」：一键复制 Base URL、API Key、推荐模型清单

### 3.4 API 网关与积分中心（F4）
- F4.1 下游 API Key：多 Key，每 Key 可设名称、模型白名单、日 / 月积分预算、RPM、启停、过期时间；统计按 Key 归集
- F4.2 积分账本：每账号余额、总额、积分包（面额 / 余额 / 到期）、企业周期额度与重置时间、签到 / 任务入账记录、请求扣费记录
- F4.3 指标：每账号消耗速度（滑动窗口）、预计耗尽时间、到期前预计浪费量、池级「未来 7 天将过期积分」
- F4.4 增强选号策略（可配置、可解释）：
  - 到期优先（EDF）：按「到期前用不完的积分」排序优先消耗，取代原固定 ×3 加权
  - 配速：按「可用积分 / 距下次补充时间」计算账号目标速率，超速账号降权
  - 成本感知：根据模型倍率估算单次消耗，低余额账号只承接免费 / 低倍率模型
  - 保底：账号级 `credit_floor`，Key 级预算熔断
  - 粘性优先，但粘性账号触底 / 超速时允许迁移
  - 每次选号记录决策原因，面板可查看「为什么选了这个号」
- F4.5 告警：积分即将过期未用完、账号全部冷却、Key 预算将耗尽（面板通知）

### 3.5 工程（F5）
- F5.1 单二进制 + Docker；配置 JSON + 环境变量覆盖；兼容读取原项目 `auths/`、`config.json`、`state.json`（迁移工具）
- F5.2 测试：保留原项目单测；核心层新增单测；前端类型检查 + 构建通过
- F5.3 git：`main` 为稳定分支，每阶段 `feat/*` 分支开发、合并；Conventional Commits 中文描述

## 4. ttswitch 对接契约

依据 `tt-switch-master/client/src-tauri/src/services/model_fetch.rs`、`proxy/usage/parser.rs`、`doc/TENCENT_REASONING_EFFORT_STRATEGY.md`。

### 4.1 模型列表 `GET /v1/models`

ttswitch 解析字段（含别名）与本项目输出：

| ttswitch 字段 | 接受别名 | 原项目输出 | 本项目输出 |
|---|---|---|---|
| `id` | - | `cn:glm-5.2` | `glm-5.2`（短名）及可选 `workbuddy/glm-5.2` |
| `owned_by` | - | `workbuddy` | Provider ID |
| `display_name` | `name` | `name` | `display_name` + `name` |
| `context_window` | `context_length` 等 | `context_length` ✅ | 只输出 `context_window`（同组别名重复会导致 ttswitch 整个列表解析失败） |
| `max_output_tokens` | `max_tokens` 等 | ✅ | ✅ |
| `input_modalities` | - | ❌（输出的是 `supports_images`） | `["text","image"]` / `["text"]` |
| `supports_tools` | `supports_function_calling` 等 | ❌（输出的是 `supports_tool_call`） | ✅ |
| `reasoning_levels` | `reasoning_efforts` | ❌（输出的是 `reasoning_supported_efforts`） | 只输出 `reasoning_levels` |

原有扩展字段（倍率 `credits`、`tags`、`vendor`、`reasoning_default_effort`）继续保留。

### 4.2 推理强度
- ttswitch 对 Chat 出站统一写顶层 `reasoning_effort`，取值 `low/medium/high/xhigh/max`
- 网关按模型实际支持集合就近映射（例如只支持 `low/medium/high` 的模型，`xhigh/max` → `high`），并在响应头 `X-Reasoning-Effort-Applied` 回显实际档位，日志记录映射
- 未传时使用模型默认档

### 4.3 用量
- 流式：ttswitch 会注入 `stream_options.include_usage`，网关必须在 `[DONE]` 前输出含 `usage` 的帧
- 缓存命中写入 `prompt_tokens_details.cached_tokens`（ttswitch 也识别 `prompt_cache_hit_tokens`）
- 扩展：`usage.credit`（本次消耗积分），不影响 ttswitch 解析

### 4.4 错误与重试
- 结构：`{"error":{"message","type","code"}}`
- 无可用账号（全冷却）：`429` + `Retry-After: <最早解冻秒数>`；全部禁用 / 无账号：`503`
- ttswitch 对 `Retry-After` 超过 5 分钟直接返回错误，因此 `Retry-After` 需如实给出，不夸大
- HTTP 200 后的流内错误必须以 `data: {"error":...}` 帧 + `[DONE]` 结束

## 5. 非功能需求
- 性能：网关额外延迟 < 5ms（不含上游），单实例支撑 50 并发流
- 安全：Bearer 常量时间比较、凭证 0600、面板 CSP、日志不落 token 与正文
- 可靠：配置原子写、状态持久化、重启恢复
- 可观测：每请求 Request-ID，选号决策可追溯

## 6. 迭代规划

| 阶段 | 分支 | 内容 | 产出 |
|---|---|---|---|
| P0 | `main` | 需求、架构文档、仓库骨架 | v0.0 ✅ |
| P1 | `feat/core-skeleton` | Go 模块、核心接口、配置、HTTP 骨架、前端骨架与 embed | v0.1.0 ✅ |
| P2 | `feat/workbuddy-provider` | 迁入原项目协议层为 WorkBuddy 适配器；添加账号、账号运维、任务、倍率模型名 | v0.2.0 ✅ |
| P3 | `feat/gateway` | 多协议入口（`/v1/messages`、`/v1/responses`）、下游 API Key、请求日志、用量分析 | v0.3.0 ✅ |
| P4 | `feat/credits` | 积分账本与预测、积分中心、到期优先选号策略、选号回放 | v0.4.0 ✅ |
| P5 | `feat/panel` | 概览整合积分与用量、账号筛选与批量操作、平台参数设置页、窄屏适配（积分中心 / 用量 / 日志 / 接入向导已在 P3、P4 完成） | v0.5.0 ✅ |
| P6 | `feat/growth` | 成长任务中心迁移、任务执行记录 | v0.6.0 ✅ |
| P7 | `chore/release` | 公开 GitHub 仓库、提交身份改为个人、CI（gofmt/vet/test/race/前端构建）、Release 工作流发布 Windows / Linux 二进制、迁移工具 | v1.0.0 |

Docker 镜像推迟到 v1.0 之后（本机开发环境不允许运行 Docker，无法验证）。

> **P0–P7 已全部完成（v1.0.0，2026-10-12）。** 此后为维护与增量演进阶段，
> 开发规则见 [DEVELOPMENT.md](DEVELOPMENT.md)，分支与版本管理见
> [BRANCHING.md](BRANCHING.md)。上表中"推迟"且至今未实现的项
> （Docker、命令面板、ECharts、第二个 Provider）列在 DEVELOPMENT.md §6。

## 7. 已确认决策（2026-10-10）
1. 先接入 WorkBuddy。不同平台的接入方式差异大，框架只规定能力接口，登录方式由适配器自行声明（见 [PROVIDER_GUIDE.md](PROVIDER_GUIDE.md)）。
2. 同时提供多种协议入口：OpenAI Chat Completions、Anthropic Messages、OpenAI Responses，满足不经 ttswitch 直接使用的场景（P3）。
3. 发布到 GitHub，优先支持 Windows 与 Linux 单二进制；Docker 后续提供。
4. 模型 ID 默认短名，兼容 `cn:` 旧前缀；新增开关 `gateway.credit_in_model_name`，开启后模型 ID 与显示名附加积分倍率（如 `glm-5.2(x0.05)`）。倍率会随限时优惠变化，网关解析时会剥离倍率后缀，客户端保存的旧名始终可用。
5. 全程 git 管理：每阶段一个 `feat/*` 分支，`--no-ff` 合并回 `main` 并打版本标签。
   **已于 2026-10-12 调整为单主干开发**（P0–P7 完成后）：只保留 `main` 长期分支，
   临时分支合并后即删，历史 `feat/*` / `fix/*` / `chore/*` 分支已清理。
   新能力仍需先写提案，见 [DEVELOPMENT.md](DEVELOPMENT.md)。
