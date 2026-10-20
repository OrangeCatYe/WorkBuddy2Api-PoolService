# 提案：快速导入到其他 Agent

日期：2026-10-20　状态：已实现（结论已并入 [REQUIREMENTS.md](../REQUIREMENTS.md) §3.8，使用说明见 [AGENT_IMPORT.md](../AGENT_IMPORT.md)）

## 实现中相对提案的调整

- 新增 `POST /api/agents/{id}/detect`：用户在向导里切换档案或手动指定目录时重新探测，提案里没有。
- 写入类接口除 `admin_key`、本机回环外，增加**同源检查**（`Origin` 与 `Host` 一致、非 `Sec-Fetch-Site: cross-site`）：`admin_key` 为空时任何网页都能 POST 到 `127.0.0.1`，必须挡住。
- DeepSeek Harness 按**档案布局**判断写入位置，而不是只看运行时版本号：本机共享运行时标 `0.1.5-rc.2`，但两个档案都在用 `cordis.patch.yml`，以布局为准更可靠。
- 提案里「受管标记」对 OMP 未明确，实现用键上的注释 `PoolService managed`，因为 OMP 不一定容忍额外字段。
- 前端发现并修复一个竞态：写入请求未返回时关闭向导并切换到另一个 Agent，迟到的结果会写进新对话框；现在用请求序号让过期结果只做刷新与提示。
- 删除唯一一项后把容器收拢成 `[]` / `{}`，使「写入再移除」尽量逐字节还原。

## 验证结果

- 本机真实配置的**副本**（Claude Code、Codex、OpenCode、DeepSeek Harness）做「写入 → 移除」，JSON 与 TOML 逐字节还原，YAML 语义一致。
- 真实 Chrome 驱动的联调服务（假模型提供方、临时 HOME）走完：Codex、Claude Code、Claude Desktop、DeepSeek Harness、OpenCode、WorkBuddy 的预览与写入，移除 Codex，窄屏无横向溢出，无浏览器错误；「没有任何密钥」分支预览不创建密钥、写入时才创建。

## 背景

把本服务接进各个 Agent 现在要手动做：复制地址和密钥，再按每个 Agent 各自的格式改配置文件。
格式五花八门（JSON、JSONC、TOML、YAML），还要自己拉模型列表、填上下文长度、思考档位。
「接入与密钥」页只给了文本片段，用户仍要自己找到配置文件并改对。

目标：在面板里选好密钥和模型，一键写进目标 Agent 的配置；可预览、可备份、可移除。

## 目标 Agent

一次覆盖九个目标（WorkBuddy 与 CodeBuddy 共用同一种配置格式）：

| Agent | 配置位置 | 协议 | 格式 |
|---|---|---|---|
| WorkBuddy | `~/.workbuddy/models.json` | Chat | JSON |
| CodeBuddy | `~/.codebuddy/models.json` | Chat | JSON |
| Claude Code | `~/.claude/settings.json` 的 `env` | Anthropic Messages | JSON |
| Claude Desktop | `Claude-3p/configLibrary/<id>.json` 等三个文件 | Anthropic Messages | JSON |
| Codex CLI | `~/.codex/config.toml` | Responses | TOML |
| OpenCode | `~/.config/opencode/opencode.json(c)` | Chat | JSONC |
| Pi Agent | `~/.pi/agent/models.json` | Chat | JSON |
| OMP | `~/.omp/agent/models.yml` | Chat | YAML |
| DeepSeek Harness | `~/.dsh/profiles/<档案>/cordis.patch.yml` 或 `settings.yaml` | Chat | YAML |

格式细节参考 ttswitch 的同类实现，但只取「定位目录、受管标记、保守写入」这几个思路，不照搬它的事务模块。

## 方案

### 定位：只认配置目录，不找安装位置

软件装在哪不重要，配置才重要。每个 Agent 按「环境变量 > 用户手动指定 > 默认位置」解析目录：
`CLAUDE_CONFIG_DIR`、`CODEX_HOME`、`OPENCODE_CONFIG`、`XDG_CONFIG_HOME`、`PI_CODING_AGENT_DIR`、
`PI_CONFIG_DIR`、`OMP_PROFILE`、`DSH_HOME`，Windows 上 Claude Desktop 另扫 `%LOCALAPPDATA%\Claude*`。
找不到时，界面允许手动指定**目录**，程序只在该目录下写固定文件名，不接受任意文件路径。

「已检测到」= 配置目录存在、命令在 `PATH` 中，或已知应用目录存在，只用于展示，不阻止写入。

### 写入：只改自己的，其余原样保留

- 受管标记：供应商统一叫 `poolservice`；`models.json` 里的条目带 `vendor: "PoolService"`；DSH 的凭据变量用 `POOLSVC_` 前缀。移除时只删带标记的。
- 同名但不是我们写的条目一律跳过并提示，不覆盖。
- 保留注释与格式：JSON/JSONC 用字节级编辑（只替换受管片段），TOML 用文本块加校验，YAML 用节点树编辑。
- 会改变「当前正在用的上游」的键（`ANTHROPIC_BASE_URL`、Codex 的 `model_provider` 等），首次写入前把原值记入 `data/agent-import/`，移除时还原。
- 写前复读：预览时记下文件内容，提交时若文件已被别的程序改过则拒绝，请重新预览。
- 写前备份到 `data/agent-backups/<agent>/<时间>/`，每个 Agent 保留最近 10 份；多文件写入失败时回滚已写的文件。
- 拒绝符号链接；解析失败不写；写完重新解析校验。

### 接口

均走现有管理密钥鉴权。

| 接口 | 作用 |
|---|---|
| `GET /api/agents` | 各 Agent 的检测结果、将写入的文件、已导入状态、可选档案 |
| `POST /api/agents/{id}/preview` | 计算变更，返回文件清单与脱敏的写入片段，不落盘 |
| `POST /api/agents/{id}/apply` | 写入 |
| `POST /api/agents/{id}/remove` | 移除受管内容并还原被改过的键 |
| `POST /api/agents/{id}/render` | 返回完整的受管片段（含密钥），用于远程部署时复制或下载 |

请求体：`key_id`、`models[]`、`default_model`、`fast_model`、`profile`、`config_dir`、`base_url`。

**本机限制**：`apply` 和 `remove` 只接受来自本机回环地址、且不带转发头的请求。
面板部署在远程服务器时，写的是服务器的文件，不是用户电脑上的，所以此时只开放预览与 `render`。

### 密钥

由用户选择，不自动为每个 Agent 建密钥。

- 界面列出可用密钥（含配置文件里的 `legacy` 密钥），默认选第一把启用的。
- 只有**一把密钥都没有**时，`apply` 才自动创建「默认密钥」（不限模型、不限额），并在结果里告知。
- 所选密钥有模型白名单时，只能导入白名单内的模型。

### 模型

沿用网关 `/v1/models` 的命名口径（`model_naming`、`credit_in_model_name`），写进 Agent 的 ID 与网关实际接受的一致。
上下文长度、最大输出、图片输入、工具调用、思考档位随模型一起写，各 Agent 取各自支持的字段。

- Claude Code：选主模型，可选快速模型，写 `ANTHROPIC_MODEL` 与 `ANTHROPIC_SMALL_FAST_MODEL`。
- Claude Desktop：只有 `claude-<家族>-…` 命名的模型会被它的模型菜单接受，其余自动跳过并说明。
- Codex、OpenCode、DSH：可选「设为默认模型」。WorkBuddy、CodeBuddy、Pi、OMP 只登记模型，在 Agent 内自行选择。

## 依赖

新增两个 Go 依赖：`gopkg.in/yaml.v3`（OMP、DSH 的 YAML 节点编辑，保留注释）和
`github.com/pelletier/go-toml/v2`（只用来校验 Codex 的 TOML 写入结果与读取现有值，不用它回写）。
都是纯 Go，不引入 cgo，不影响单二进制交叉编译。

## 影响面

| 项 | 影响 |
|---|---|
| `config.json` | 无变化 |
| `/v1/models` 与网关协议 | 无变化 |
| 现有 API | 只新增，不改动 |
| 数据目录 | 新增 `agent-import/`（还原用的原值）和 `agent-backups/` |
| 能力接口 | 无，Agent 导入与平台无关，不进 Provider 接口 |

## 不做

- 不改 Agent 的安装与启动，不杀进程，写完提示用户重启对应应用。
- 不写操作系统环境变量、不改注册表。
- 不接管 ttswitch 已管理的内容；检测到 Claude Desktop 已被另一个工具接管时给出提示。
- 不自动轮询或后台同步，模型目录变化后用户需要重新导入。

## 风险与限制

- WorkBuddy、CodeBuddy 的 `url` 字段是否需要带 `/chat/completions`，官方文档与社区教程说法不一，按官方文档写完整路径，需要用户在实际应用里确认一次。
- 本机只有部分 Agent 装有软件：OMP、Pi 没有安装，Linux/macOS 路径只靠单元测试覆盖，无法实机验证。
- YAML 回写会丢失空行（注释保留），写前有备份。

## 验证

- JSONC 编辑器：注释、尾逗号、BOM、CRLF、单行对象、空对象、深层路径、重复写入幂等。
- 每个 Agent 一组测试：新建文件、合并进已有配置、更新、跳过冲突、移除后与原文件逐字节一致（对只增不改的目标）。
- 还原类目标：写入后移除，被改过的键回到原值。
- 提交器：内容被并发修改时拒绝、多文件失败回滚、符号链接拒绝、备份保留数量。
- 接口：未登录、`admin_key` 为空、非本机请求、密钥选择与自动创建、白名单过滤。
- 用本机真实配置的**副本**做一次回环检查，确认其余内容不变。
- 前端类型检查与构建，浏览器里走一遍预览与写入流程。
