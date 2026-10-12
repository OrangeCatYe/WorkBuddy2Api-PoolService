# PoolService

多平台 AI 编程助手账号池网关：把 WorkBuddy / CodeBuddy 等软件的账号聚合成统一的 OpenAI 兼容 API，供 ttswitch 等 Agent 路由工具接入，并提供积分中心与 Web 管理面板。

> 基于 [workbuddy2api-panel](https://github.com/linguo2625469/workbuddy2api-panel)（MIT）重构，仅限管理本人账号使用，请遵守各平台服务协议。

## 功能

- **多平台框架**：平台以适配器接入，登录方式、账号操作、定时任务由适配器声明，面板自动渲染。当前支持 WorkBuddy（国内版 / 国际版）。
- **多协议网关**：OpenAI Chat Completions、Anthropic Messages、OpenAI Responses 三种入口，ttswitch、Claude Code、Codex CLI 都能直接接入；模型能力字段与 ttswitch 对齐，思考强度五档自动映射。
- **下游密钥**：按密钥限制模型、RPM 与每日 / 累计积分，独立统计用量。
- **请求日志与用量分析**：每个请求的账号、token、缓存命中、积分、耗时一目了然（不记录对话内容）。
- **积分中心**：预测每批积分到期前能否用完，按到期压力把收费请求优先分给快过期的账号，每次选号都能回放原因。详见 [docs/CREDITS.md](docs/CREDITS.md)。
- **模型名显示积分倍率**：可选在模型名后附加倍率，如 `glm-5.2(x0.05)`，在 Agent 里直接看到成本。
- **账号池**：沿用原项目的冷却、熔断、会话粘性、快过期积分优先等策略；签到、保活、猫猫旅行等定时任务。
- **Web 面板**：概览、账号（批量操作、积分包到期）、模型目录、任务、设置；明暗主题。

## 快速开始

从 Release 下载对应平台的二进制，或自行构建：

```bash
# Windows
powershell -ExecutionPolicy Bypass -File scripts/build.ps1
# Linux
scripts/build.sh
```

首次运行自动生成 `config.json`，并在日志中打印面板管理密钥：

```bash
./bin/poolsvc -config config.json
# 面板: http://127.0.0.1:7863
```

在面板「账号 → 添加账号」中选择「国内版授权」，在弹出的页面完成登录，账号即自动加入池中。

## 配置

```jsonc
{
  "listen": "127.0.0.1:7863",
  "data_dir": "./data",
  "admin_key": "adm-...",                 // 面板密钥
  "gateway": {
    "legacy_api_key": "sk-...",           // 下游 Agent 使用的 API Key
    "model_naming": "short",              // short / full / both
    "credit_in_model_name": false         // 模型名附加积分倍率
  },
  "providers": {
    "workbuddy": {
      "enabled": true,
      "auth_dir": "",                     // 空 = data/workbuddy/auths
      "pool": { "credit_floor": 100 },
      "schedule": { "checkin_hours": [9, 21] }
      // 其余字段与原 workbuddy2api-panel 的 config.json 同名同义
    }
  }
}
```

环境变量 `POOLSVC_LISTEN`、`POOLSVC_DATA_DIR`、`POOLSVC_ADMIN_KEY` 可覆盖对应字段。

## 接入 ttswitch

在 ttswitch 中新增「自定义 OpenAI 兼容供应商」：

| 项 | 值 |
|---|---|
| Base URL | `http://127.0.0.1:7863/v1` |
| API Key | `gateway.legacy_api_key` |
| API 格式 | Chat Completions |

拉取模型列表后，上下文长度、最大输出、图片输入、工具调用、思考档位会自动识别。思考强度按 ttswitch 的 `low / medium / high / xhigh / max` 发送，网关映射到模型实际支持的档位，并在响应头 `X-Reasoning-Effort-Applied` 中给出实际档位。

## 接入 Claude Code / Codex CLI

面板「接入与密钥」页提供可直接复制的配置片段。简要说明：

```bash
# Claude Code（Anthropic Messages）
export ANTHROPIC_BASE_URL="http://127.0.0.1:7863"
export ANTHROPIC_AUTH_TOKEN="sk-pool-..."
export ANTHROPIC_MODEL="glm-5.2"
```

```toml
# Codex CLI（~/.codex/config.toml，Responses 协议）
model = "glm-5.2"
model_provider = "poolservice"

[model_providers.poolservice]
name = "PoolService"
base_url = "http://127.0.0.1:7863/v1"
wire_api = "responses"
env_key = "POOLSVC_API_KEY"
```

协议转换的能力边界见 [docs/API.md](docs/API.md)。

## 从 workbuddy2api-panel 迁移

用内置迁移工具一条命令完成（旧版目录指到 v1.x 的解压目录）：

```bash
go run ./cmd/migrate -from /path/to/wb2api-panel-v1.11.7-windows-amd64

# 先看看会做什么，不写文件
go run ./cmd/migrate -from /path/to/old -dry-run
```

工具会把旧版 `auths/` 下的凭证与 `data/state.json` 里的运行态（余额、禁用、冷却、
统计、模型冷却、成本账本）一并迁入，迁移前自动备份现有 `auths` 与 `state.json`
到 `data/backup/`。同 UID 已存在时保留令牌较新的一方，避免用过期凭证覆盖有效
凭证；加 `-force` 改为旧版无条件覆盖。

迁移后重启服务生效。旧版客户端使用的 `cn:glm-5.2` 这类模型名仍然可用。

手工迁移（工具不适用时）参照：

1. 把旧项目的 `auths/` 目录复制到 `data/workbuddy/auths/`，或在配置中设置 `providers.workbuddy.auth_dir` 指向旧目录。
2. 可选：把旧 `data/state.json` 复制到 `data/workbuddy/state.json`，保留冷却与统计状态。
3. 旧 `config.json` 中的 `pool`、`schedule`、`upstream`、`global`、`prompt` 等段落原样放进 `providers.workbuddy`。

## 下载

从 [Releases](https://github.com/OrangeCatYe/WorkBuddy2Api-PoolService/releases) 下载
对应平台压缩包，解压后复制配置模板并启动：

```bash
cp config.example.json config.json
./poolsvc            # Windows 用 pools.exe
```

压缩包附带 `.sha256` 校验文件：`sha256sum -c poolsvc-linux-amd64.tar.gz.sha256`。

## 开发

```bash
npm --prefix web install
npm --prefix web run dev          # 前端开发服务器，代理到 127.0.0.1:7863
go test ./...
```

打 `v*` 标签会自动触发 Release 工作流，交叉编译 Windows / Linux 产物并上传。

- 需求：[docs/REQUIREMENTS.md](docs/REQUIREMENTS.md)
- 架构：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- 新平台接入：[docs/PROVIDER_GUIDE.md](docs/PROVIDER_GUIDE.md)
- 变更记录：[CHANGELOG.md](CHANGELOG.md)
