# 后续开发规则

v1.0 之前的需求规划（P0–P7，见 [REQUIREMENTS.md](REQUIREMENTS.md) §6）已全部完成，
仓库进入**维护与增量演进**阶段。本文规定此后的开发如何组织和落地。

配套文档：[分支与版本管理](BRANCHING.md)（提交、标签、发版流程）、
[架构](ARCHITECTURE.md)、[新平台接入](PROVIDER_GUIDE.md)。

## 一、当前阶段定位

不再有"大版本集中开发"的节奏。后续改动分三类，处理方式不同：

| 类型 | 典型 | 处理 |
|---|---|---|
| 修复 | 崩溃、解析错误、策略不准 | 直接改，补测试，升 `PATCH` |
| 增强 | 在现有能力上改进（显示、策略、性能） | 直接改，升 `MINOR` |
| 新能力 | 新页面、新协议、新 Provider | 先写提案再动手，见 §2 |

判断标准：**是否需要新增对外契约（API、配置结构、Provider 能力接口）**。
需要 → 走提案；不需要 → 直接改。

## 二、提案流程（仅新能力需要）

改动前先写一份简短提案，放在 `docs/proposals/YYYY-MM-DD-<主题>.md`，不必长，
说清四件事即可：

1. **背景**：解决什么问题，现在为什么不成立
2. **方案**：怎么做，涉及哪些模块，是否新增接口或配置项
3. **影响面**：是否破坏兼容（现有 `config.json`、`/v1/models` 输出、ttswitch 解析）
4. **验证**：如何确认做对了

提案确认后再实现，实现完把提案结论并入 [REQUIREMENTS.md](REQUIREMENTS.md) 的对应
章节，提案文件可保留作决策留档。

**为什么要这一步**：v1.0 已发布，下游有真实使用（ttswitch 接入、Release 二进制）。
破坏兼容的改动代价变高了，先对齐再动手比事后返工便宜。

## 三、不可破坏的红线

以下是 v1.0 已确立、被下游依赖或有测试保护的契约，改动前必须确认不会破坏：

### 1. ttswitch 解析契约

`/v1/models` 的同组别名**不能重复出现**。`display_name`/`name`、
`context_window`/`context_length`、`reasoning_levels`/`reasoning_efforts`
每组只能输出一个，重复会让 ttswitch 整个模型列表解析失败（v0.6.1 修复过）。

保护测试：`internal/gateway/proto_test.go`。改模型输出字段前先看这个测试。

### 2. 上游协议适配

请求改写管线（`internal/gateway/rewrite*.go`、`internal/providers/.../upstream/`）
的每一项都对应一个具体上游行为，改之前确认知道它防的是什么。典型如
`developer` 角色归一、首包/流式超时、DSML 工具调用还原。

### 3. 公开配置结构

`config.json` 的字段语义一旦发布就要保持兼容。新增字段用可选、有默认值；
改语义或删字段视为破坏性变更，升 `MAJOR`。

### 4. 数据兼容

`data/workbuddy/state.json`、`auths/`、`usage.json` 的格式要能被新旧版本读取。
改动持久化结构时考虑老数据如何升级，必要时扩展 `cmd/migrate`。

### 5. 发布脱敏

`CHANGELOG.md` 与对外文档不得出现内部开发代号；`scripts/sanitize_changelog.py`
在残留时以非零码退出，CI 会失败。写入前用中性表述。

## 四、回归防护

现有 **82 个测试文件**是回归网。规则：

- 修复缺陷时**同时补一个能复现该缺陷的测试**，防止再次退化
- 改公共行为前先跑 `go test ./...`，确认基线是绿的
- 不为了通过测试而放宽断言；测试失败先判断是代码错还是测试过期

提交前跑预检（约 15 秒），覆盖除 `go test -race` 外的全部 CI 检查：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/precheck.ps1
```

## 五、文档同步

改了行为就要同步文档，否则下次会基于错误认知做判断。对应关系：

| 改动 | 更新 |
|---|---|
| 对外接口、协议字段 | [API.md](API.md) |
| 积分模型、选号策略 | [CREDITS.md](CREDITS.md) |
| 模块结构、数据流 | [ARCHITECTURE.md](ARCHITECTURE.md) |
| Provider 能力接口、接入方式 | [PROVIDER_GUIDE.md](PROVIDER_GUIDE.md) |
| 用户可见变更 | `CHANGELOG.md` 的 `[未发布]` 段 |
| 需求变更或新决策 | [REQUIREMENTS.md](REQUIREMENTS.md) |

另外：写完代码回头看一眼相关注释是否还成立。**注释与代码不一致比没注释更糟**——
本项目注释密度高且多记录"为什么这么写"（上游行为的坑、竞态修复背景），
改代码时容易漏改注释，留着会误导后续判断。

## 六、已知遗留

v1.0 规划中标注"推迟"、目前未实现的项，做之前先确认是否仍需要：

| 项 | 出处 | 状态 |
|---|---|---|
| Docker 镜像 | REQUIREMENTS §5 F5.1、§6 备注、§7 决策 3 | 未做。本机无 Docker 环境无法验证，做之前先解决验证手段 |
| 全局搜索 / 命令面板（Ctrl+K） | REQUIREMENTS §3 F2.3 | 未做。当前各页有独立搜索框 |
| ECharts 图表 | REQUIREMENTS §3 F2.1 | 未引入该依赖，图表为自绘。引入前先确认是否真需要 |
| 多平台 Provider（第二个平台） | REQUIREMENTS §3 F1.5 | 框架已就绪，仅接入了 WorkBuddy |

这些是**当时明确推迟**的，不是遗漏。做之前先补提案，确认现在还值不值得做。

## 七、日常节奏

1. 改动落在 `main`，一个提交做一件事（提交规范见 [BRANCHING.md](BRANCHING.md)）
2. 改完跑预检
3. 同步受影响文档与 `CHANGELOG.md` 的 `[未发布]` 段
4. 推送，**不等待 CI**；报错由人工贴回错误信息再修
5. 攒够一批改动后按 [BRANCHING.md](BRANCHING.md) §5 发版

唯一需要特意看 CI 的情况：改动涉及 goroutine、共享状态、并发写——
`go test -race` 本机跑不了（无 gcc），只有 CI 能覆盖。
