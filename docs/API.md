# API 参考

PoolService 对外提供三种协议入口，共用同一套鉴权、限额、模型路由与用量记录。

| 入口 | 协议 | 典型客户端 |
|---|---|---|
| `POST /v1/chat/completions` | OpenAI Chat Completions | ttswitch、Cherry Studio、各类 SDK |
| `POST /v1/messages` | Anthropic Messages | Claude Code、Anthropic SDK |
| `POST /v1/messages/count_tokens` | Anthropic（估算） | Claude Code |
| `POST /v1/responses` | OpenAI Responses | Codex CLI、OpenAI Agents SDK |
| `GET /v1/models` | OpenAI | 所有客户端 |
| `GET /healthz` | - | 探活（无需鉴权） |

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
