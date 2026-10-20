# 快速导入到其他 Agent

左侧「网关」组的「快速导入」把本网关的地址、密钥和模型一键写进其他 Agent 的配置，不用再手动找配置文件、拼格式、查上下文长度。

## 使用

1. 在页面顶部选好**使用的密钥**（所选密钥有模型白名单时，只能导入白名单内的模型）；Agent 与本服务不在同一台电脑时，改一下**网关地址**。
2. 点要导入的 Agent 卡片上的「导入」，勾选模型；Claude Code 选主模型，Codex、OpenCode、DeepSeek Harness 可选「设为默认模型」。
3. 点「预览变更」，核对将写入的文件和内容（密钥已遮掩）。
4. 点「写入」。完成后按提示重启对应的应用。

写入后卡片显示「已导入 N 个模型」，之后可以「更新」（重新选择模型）或「移除」。

还没有任何密钥时，写入会自动创建一把「默认密钥（全部模型）」（不限模型、不限额）；已经有密钥时必须自己选，不会再悄悄新建。

## 支持的 Agent

| Agent | 配置位置 | 可用环境变量 | 协议 |
|---|---|---|---|
| WorkBuddy | `~/.workbuddy/models.json` | - | OpenAI Chat |
| CodeBuddy | `~/.codebuddy/models.json` | - | OpenAI Chat |
| Claude Code | `~/.claude/settings.json` 的 `env` | `CLAUDE_CONFIG_DIR` | Anthropic Messages |
| Claude Desktop | `Claude-3p/configLibrary/<id>.json`、`_meta.json`、两份 `claude_desktop_config.json` | - | Anthropic Messages |
| Codex CLI | `~/.codex/config.toml` | `CODEX_HOME` | OpenAI Responses |
| OpenCode | `~/.config/opencode/opencode.json`（或 `.jsonc`） | `OPENCODE_CONFIG`、`XDG_CONFIG_HOME` | OpenAI Chat |
| Pi Agent | `~/.pi/agent/models.json` | `PI_CODING_AGENT_DIR` | OpenAI Chat |
| OMP | `~/.omp/agent/models.yml` | `PI_CONFIG_DIR`、`PI_CODING_AGENT_DIR`、`OMP_PROFILE` | OpenAI Chat |
| DeepSeek Harness | `~/.dsh/profiles/<档案>/cordis.patch.yml`，旧版为 `~/.dsh/settings.yaml`；密钥在 `~/.dsh/.credentials.yaml` | `DSH_HOME` | OpenAI Chat |

Windows 下 `~` 是用户目录；Claude Desktop 的数据目录是 `%LOCALAPPDATA%\Claude` 与 `Claude-3p`（macOS 在 `~/Library/Application Support`）。

### 软件装在别处怎么办

只看**配置目录**，软件装在哪个盘无所谓。顺序是：手动指定 > 环境变量 > 默认位置。卡片显示「未检测到」只代表没找到配置或命令，不影响写入。

找不到配置时，在导入窗口底部展开「找不到配置？手动指定配置目录」，填配置所在的**目录**（不是文件）。程序只会在该目录下写固定文件名的文件。

### 写入内容

- **WorkBuddy / CodeBuddy**：往 `models` 数组追加条目，条目带 `"vendor": "PoolService"` 作标记；`availableModels` 存在时同步追加。`url` 写完整的 `…/v1/chat/completions`，这是官方文档要求的写法。
- **Claude Code**：写 `env` 里的 `ANTHROPIC_BASE_URL`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_MODEL`，选了快速模型时再写 `ANTHROPIC_SMALL_FAST_MODEL`。这是全局设置，写入后所有会话都改走本网关。
- **Claude Desktop**：用 3P（第三方网关）模式，写一份独立档案并设为当前启用，把 `deploymentMode` 切到 `3p`。模型菜单只接受 `claude-<家族>-…` 命名的模型，其余自动跳过。
- **Codex CLI**：在 `# >>> PoolService managed >>>` 标记块里写 `[model_providers.poolservice]`；勾选「设为默认」时再改顶层 `model` 与 `model_provider`。
- **OpenCode**：写 `provider.poolservice`（`@ai-sdk/openai-compatible`）；勾选「设为默认」时改顶层 `model`。
- **Pi Agent / OMP**：写 `providers.poolservice`，在会话内用 `/model` 选择。
- **DeepSeek Harness**：在所选档案的 `llm-pi-ai` 条目下写 `providers.poolservice`，密钥通过 `apiKeyEnv` 引用 `.credentials.yaml` 里的 `POOLSVC_DSH_API_KEY`；勾选「设为默认」时改 `agent-default-model` 条目。写入位置按档案布局判断，而不是只看运行时版本号。

## 安全与保护

- **只改自己的**：用户原有的供应商、模型、注释和排版原样保留。同名但不是本工具写入的条目一律跳过并提示，不覆盖。
- **先备份**：写入前把原文件备份到 `data/agent-backups/<agent>/<时间>/`，每个 Agent 保留最近 10 份。
- **复读校验**：预览之后文件被别的程序改过，写入会被拒绝（409），请重新预览。
- **原子写入与回滚**：多文件写入中途失败，已写的文件会还原；写完重新解析校验。
- **拒绝符号链接、解析失败的文件**，不写任何内容。
- **接管类改动可还原**：被覆盖的原值记录在 `data/agent-import/<agent>.json`，移除时还原。该文件含你原来的配置值，和 `keys.json` 一样按敏感文件对待。
- **写入只在本机**：要求已设置 `admin_key`、请求来自本机回环地址、浏览器请求同源。面板部署在远程服务器时只能预览，用「显示完整配置」复制后手动粘贴。

## 移除

点已导入卡片上的「移除」：删除本工具写入的供应商和模型，还原被改过的原值，其余原样保留。如果导入时手动指定过配置目录，移除时也要填同一个目录。

## 常见问题

**Claude Desktop 写入后又被切回去了。** ttswitch 等工具也在管理 Claude Desktop 时，它们再次写入会把启用档案切回去。同一时间请只让一个工具管理它。

**导入后模型不出现。** WorkBuddy / CodeBuddy 需要完全退出（包括托盘图标）再打开；Claude Desktop 同理。修改 `models.json` 后部分版本会自动热加载。

**WorkBuddy 提示地址不对。** 官方文档要求 `url` 写完整的 `/chat/completions` 路径，社区教程有不同说法。如果你的版本不接受，请在应用里改一次并反馈。

**WorkBuddy / CodeBuddy 里没有思考强度选择。** 支持思考的模型会写入 `reasoning.supportedEfforts`（目录声明的全部档位）和 `reasoning.defaultEffort`（目录声明的默认档，没有声明时取 high、medium，再不行取最后一档）。目录没有给出档位的模型只写 `supportsReasoning`，不会凭空编造档位。已导入过的用户在卡片上点「更新」即可补上。

**OMP 提示发现旧版配置。** 只有旧版 `models.json` 而没有 `models.yml` 时，需要先启动一次 OMP 让它自己迁移，再回来导入。

**模型目录变了怎么办。** 本功能不会自动同步，模型变化后在卡片上点「更新」重新选择即可。

## 已知限制

- YAML 文件（OMP、DeepSeek Harness）回写会丢失空行，注释保留；写前有备份。
- OMP、Pi 在本机没有安装，Linux / macOS 路径只靠单元测试覆盖，没有实机验证。
- Claude Desktop 目前只支持 Windows 和 macOS。
- 不支持 OpenCode 新版的 `providers` 结构，遇到会拒绝写入并说明。
