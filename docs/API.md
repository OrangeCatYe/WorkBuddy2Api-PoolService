# API 参考

PoolService 对外提供三种协议入口，共用同一套鉴权、限额、模型路由与用量记录。

| 入口 | 协议 | 典型客户端 |
|---|---|---|
| `POST /v1/chat/completions` | OpenAI Chat Completions | ttswitch、Cherry Studio、各类 SDK |
| `POST /v1/messages` | Anthropic Messages | Claude Code、Anthropic SDK |
| `POST /v1/messages/count_tokens` | Anthropic（估算） | Claude Code |
| `POST /v1/responses` | OpenAI Responses | Codex CLI、OpenAI Agents SDK |
| `GET /v1/models` | OpenAI | 所有客户端 |
| `GET /healthz` | - | 探活（无需鉴权）。响应 `{"service","version","healthy","total"}`；账号池里没有可用账号时状态码为 503，但仍带 `version`，软件更新靠它确认新版本已启动 |

## 鉴权

依次读取以下请求头，任一匹配即可：

- `Authorization: Bearer <key>`
- `x-api-key: <key>`（Anthropic 风格）
- `api-key: <key>`（Azure 风格）

可用的密钥有两类：面板「接入与密钥」中创建的密钥（`sk-pool-` 前缀，可设限额），以及 `config.json` 中的 `gateway.legacy_api_key`（不受限额约束）。

## 模型名

- 默认短名，如 `glm-5.2`；多平台同名时其余平台用 `平台/模型`
- 兼容旧前缀：`cn:glm-5.2`、`global:gpt-5.4`
- 开启 `credit_in_model_name` 后列表中的名字带倍率，如 `glm-5.2(x0.05)`；请求时带不带后缀都可以
- 响应中的 `model` 字段原样回显请求时使用的名字

## 推理强度

| 来源 | 字段 |
|---|---|
| Chat Completions | `reasoning_effort`（也兼容 `reasoning.effort`） |
| Responses | `reasoning.effort` |
| Messages | `output_config.effort`；或 `thinking.budget_tokens` 推断：<4K low，<16K medium，<32K high，其余 xhigh |

档位按模型实际支持集合就近映射，响应头 `X-Reasoning-Effort-Applied` 给出实际档位，发生映射时 `X-Reasoning-Effort-Requested` 给出原档位。

## 协议转换说明

Messages 与 Responses 在网关内部转换为 Chat Completions 后转发，响应再转换回来。

| 能力 | Messages | Responses |
|---|---|---|
| 文本 / 图片输入 | ✅ base64 与 URL | ✅ `input_text` / `input_image` |
| 工具调用 | ✅ `tool_use` / `tool_result`，流式 `input_json_delta` | ✅ `function_call` / `function_call_output` |
| 思考输出 | ✅ `thinking` 块 | ✅ `reasoning` 项（`summary_text`） |
| 结构化输出 | - | ✅ `text.format` json_schema / json_object |
| usage | `input_tokens` 不含缓存，缓存计入 `cache_read_input_tokens` | `input_tokens` 含缓存，缓存见 `input_tokens_details.cached_tokens` |
| 服务端工具 | ❌ web_search 等会被忽略 | ❌ 内置工具会被忽略 |
| 有状态会话 | - | ❌ 不支持 `previous_response_id`，需发送完整上下文（Codex 默认如此） |

`thinking` 块的 `signature` 为空字符串；客户端回传的 thinking 块会作为 `reasoning_content` 转发。

## 错误

各协议使用各自的错误格式（OpenAI：`{"error":{...}}`；Anthropic：`{"type":"error","error":{...}}`）。

| 状态码 | 含义 |
|---|---|
| 401 | 密钥无效、已停用或已过期 |
| 403 | 密钥不允许使用该模型 |
| 404 | 模型不存在 |
| 429 | 账号全部冷却、密钥 RPM 超限或积分预算用完；带 `Retry-After` |
| 503 | 无可用账号，或模型目录暂时拉取失败（带 `Retry-After`） |

流式响应开始后发生的错误，以协议对应的错误事件结束（Chat：`data: {"error":...}`；Messages：`event: error`；Responses：`response.failed`）。

## 请求日志

每个请求记录模型、账号、密钥、token、积分、耗时与状态，**不记录提示词与回答正文**。内存保留最近 5000 条供面板查询，同时按日写入 `data/requests/requests-YYYY-MM-DD.jsonl`，保留 30 天。

## 平台管理接口

面板使用的管理接口需要 `admin_key`（请求头 `Authorization: Bearer <admin_key>`）。以下两组可用于脚本化运维：

| 接口 | 说明 |
|---|---|
| `POST /api/providers/{pid}/tasks/{task}/run` | 立即执行任务。请求体 `{"account_ids": [...]}` 可选，省略时作用于全部账号 |
| `GET /api/providers/{pid}/settings` | 平台参数：`fields`（声明）、`values`（已保存值，密钥不回显）、`pending_restart` |
| `GET /api/task-runs` | 执行历史，可按 `provider`、`task`、`status`（`running / ok / partial / failed / skipped`）筛选，`limit` 最大 500 |
| `GET /api/task-runs/latest` | 每个任务最近一次执行的摘要 |
| `GET /api/task-runs/{id}` | 单次执行详情，含逐账号结果 |
| `GET /api/providers/{pid}/accounts/{aid}/quests` | 账号的成长任务与进度 |
| `POST /api/providers/{pid}/accounts/{aid}/quests/run` | 自动完成任务，请求体 `{"codes": [...]}` 可选，省略为全部待办；返回 `run_id` |
| `POST /api/providers/{pid}/accounts/{aid}/quests/{code}/claim` | 领取奖励 |
| `POST /api/providers/{pid}/accounts/{aid}/quests/accept` | 接受全部未接受的任务 |
| `PUT /api/providers/{pid}/settings` | 修改参数，只提交改动项：`{"values": {"pool.max_in_flight": 4}}`；校验失败返回 400 且不改动任何状态 |
| `POST /api/providers/{pid}/export` | 导出所选账号的配置。请求体 `{"account_ids": [...]}`；返回 `{"count", "missing", "accounts"}`，其中 `accounts` 即可保存为文件、并在「添加账号 → 导入凭证」中原样导入的内容。**含登录令牌明文**，详见下节 |
| `GET /api/update/status` | 软件更新状态：当前版本、最新版本、状态机、下载进度、上次更新结果 |
| `POST /api/update/check` | 检查更新。请求体 `{"source": "<下载源ID>"}` 可省略；网络与校验失败体现在返回的 `state=error` / `error`，不是 HTTP 错误 |
| `POST /api/update/download` | 后台下载并校验最新版本，进度从 `status` 轮询。需要 `admin_key` |
| `POST /api/update/apply` | 安装已下载的版本并重启服务，返回 202。需要 `admin_key` |
| `GET /api/settings/update` / `PUT /api/settings/update` | 下载源、代理、自动检查。`PUT` 需要 `admin_key`；代理地址里的密码读取时显示为 `***`，原样提交表示不修改 |

### 导出账号配置

`accounts` 是凭证数组，每项与 `auths/*.json` 的落盘形态一致：

```json
[
  {
    "auth": { "accessToken": "…", "refreshToken": "…", "expiresAt": 1790000000, "domain": "copilot.tencent.com", "realm": "cn" },
    "account": { "uid": "…", "enterpriseId": "", "nickname": "…" }
  }
]
```

- 只导出请求里列出的账号，顺序与请求一致；重复的 ID 会合并。不存在或凭证不完整的 ID 不会中断整批，而是列在 `missing` 里。所选账号全部无法导出时返回 400。
- 不含余额、冷却、统计等运行态；导入后会重新拉取。整体迁移旧版运行态用 `cmd/migrate`。
- 导入同一 UID 时，如果池中现有凭证的过期时间不早于导入内容，该账号会被跳过（避免旧导出覆盖已轮换的刷新令牌）；导入内容更新时照常覆盖。
- **安全**：响应带 `Cache-Control: no-store`；仅支持 POST，令牌不会出现在 URL 中；`admin_key` 为空时返回 403 拒绝导出；服务端日志只记录数量，不记录任何令牌。导出文件等同账号密码，请妥善保管。
- 平台需要实现 `AccountExporter` 才有此接口，否则返回 400。当前 WorkBuddy 已支持。

积分相关接口见 [CREDITS.md](CREDITS.md)。

### 软件更新

完整说明见 [UPDATE.md](UPDATE.md)。`GET /api/update/status` 的返回：

```json
{
  "state": "available",
  "source": "github",
  "checked_at": "2026-10-20T10:00:00Z",
  "current": { "version": "v1.1.0", "release": true, "can_apply": true, "mode": "self", "platform": "windows-amd64" },
  "latest": { "version": "1.2.0", "tag": "v1.2.0", "size": 6291456, "signed": true, "installable": true, "notes": "…" },
  "progress": { "done": 1000000, "total": 6291456 },
  "last_result": { "ok": true, "from": "v1.1.0", "to": "v1.2.0", "message": "已更新到 v1.2.0", "at": "…" }
}
```

- `state`：`idle`（未检查）/ `checking` / `up_to_date` / `available` / `downloading` / `ready`（已下载并校验）/ `applying` / `error`。
- `current.can_apply` 为 `false` 时 `reason` 说明原因：开发版、安装目录不可写、未设置 `admin_key`。
- `latest.installable` 为 `false` 表示清单没有有效签名或没有本平台安装包，只能手动下载。清单**有签名但验不过**不会出现在这里，而是 `state=error`。
- `last_result` 在服务重启后由新进程读取更新程序写下的结果，`rolled_back=true` 表示新版本没能启动、已回滚。
- 错误码：`400` 前置条件不满足（未检查就下载、开发版、未签名等），`403` `admin_key` 为空，`409` 正在进行其他更新操作。
- 安装接口返回 202 后服务会在约 0.5 秒内开始退出，客户端应轮询 `status` 直到新进程应答。

### 快速导入

把网关地址、密钥和模型写进其他 Agent 的配置。使用说明与各 Agent 的格式见 [AGENT_IMPORT.md](AGENT_IMPORT.md)。

| 接口 | 说明 |
|---|---|
| `GET /api/agents` | 各 Agent 的检测结果（是否检测到、将写入的文件、可选档案、是否已导入）、可导入的模型、网关地址，以及当前请求能否写入 `can_write` / `write_block`。`?key_id=` 让模型按该密钥的白名单过滤 |
| `POST /api/agents/{id}/detect` | 按指定档案或配置目录重新检测。请求体 `{"profile": "", "config_dir": ""}` 均可省略 |
| `POST /api/agents/{id}/preview` | 计算变更但不落盘。返回文件清单（`create / update / delete / unchanged`）与**已遮掩密钥**的受管片段 |
| `POST /api/agents/{id}/render` | 同 `preview`，但片段含完整密钥，用于远程部署时复制。还没有任何密钥时返回 400 |
| `POST /api/agents/{id}/apply` | 写入。返回写入的文件与备份目录；没有任何密钥时自动创建默认密钥，`key.created` 为 `true` |
| `POST /api/agents/{id}/remove` | 移除本工具写入的内容并还原被改过的原值。请求体 `{"profile": "", "config_dir": ""}` |

`{id}` 取值：`workbuddy`、`codebuddy`、`claude-code`、`claude-desktop`、`codex`、`opencode`、`pi`、`omp`、`deepseek-harness`。

`preview` / `render` / `apply` 的请求体：

```json
{
  "key_id": "key_ab12cd",
  "models": ["glm-5.2", "hy3"],
  "default_model": "glm-5.2",
  "fast_model": "",
  "opus_model": "",
  "sonnet_model": "",
  "profile": "",
  "config_dir": "",
  "base_url": ""
}
```

- `key_id`：下游密钥 ID，`legacy` 表示配置文件里的 `legacy_api_key`。**必须由调用方指定**；只有一把密钥都没有时才可留空，此时 `apply` 自动创建一把不限模型、不限额的默认密钥。
- `models`：沿用 `/v1/models` 的命名（受 `model_naming`、`credit_in_model_name` 影响）。所选密钥有白名单时，白名单外的模型进入返回的 `skipped`，不会中断整批。
- `default_model`：必须在 `models` 内。Claude Code 必须指定；Codex、OpenCode、DeepSeek Harness 为可选的「设为默认」；其余 Agent 忽略。
- `fast_model` / `opus_model` / `sonnet_model`：仅 Claude Code 使用，分别对应 Haiku、Opus、Sonnet 三个档位别名，必须在 `models` 内；留空表示跟随 `default_model`。
- `profile`：DeepSeek Harness / OMP 的档案名，必须是实际存在的档案，不接受路径。
- `config_dir`：配置**目录**，只会在其下写固定文件名，不接受任意文件路径。
- `base_url`：网关对外地址，省略时由请求推导（本机请求会把 `localhost` 换成 `127.0.0.1`）。

返回（`preview` / `apply`）：

```json
{
  "agent": "codex",
  "files": [{ "path": "…/config.toml", "action": "update", "lang": "toml", "snippet": "…" }],
  "imported": ["glm-5.2"],
  "skipped": [{ "id": "hy3", "reason": "所选密钥没有使用这个模型的权限" }],
  "warnings": [],
  "key": { "id": "key_ab12cd", "name": "ttswitch" },
  "backup_dir": "…/data/agent-backups/codex/20261020-110950.630",
  "written": ["…/config.toml"],
  "restart": "新开的 codex 会话生效"
}
```

- **`apply` 与 `remove` 只在本机可用**：要求 `admin_key` 已设置（否则 403，防止任何网页跨站借用接口改你的配置）、请求来自本机回环地址且不带 `X-Forwarded-*` 头、浏览器请求的 `Origin` 与 `Host` 一致。其余接口不受后两条限制。
- 错误码：`400` 参数或配置不合法（文件无法解析、同名条目不是本工具写入的、没有可导入的模型等），`403` 不满足写入条件，`404` 未知 Agent，`409` 配置文件在预览之后被其他程序改过，请重新预览。
- 响应里的 `snippet` 在 `preview` / `apply` 中密钥已遮掩；日志只记录 Agent、模型数量和文件数量，不记录密钥。
