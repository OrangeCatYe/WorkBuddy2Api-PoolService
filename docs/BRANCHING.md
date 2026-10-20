# 分支与版本管理规则

本仓库采用**单主干开发（trunk-based）**：只有一条长期分支 `main`，所有开发直接在
`main` 上进行，版本以标签发布。本文规定这套模式的执行细节。

## 一、分支

### 长期分支

只有 `main`。它**始终可发布**：即任何时刻从 `main` 切出都能构建出一个可用版本。

这条约束的实际含义是：不要把"改到一半、会编译失败或功能残缺"的状态留在 `main` 上。
大改动请在一个提交内完成自洽的单元（例如一次完整的修复 = 代码 + 测试 + 变更记录），
而不是分成"先提交一半"多个提交。

### 允许开临时分支的情形

满足以下任一条件时才从 `main` 切临时分支：

- 破坏性重构，中途状态会长时间不可用
- 需要并行推进、互不干扰的两项以上改动
- 需要他人评审的改动

临时分支命名沿用已有风格：

| 前缀 | 用途 | 示例 |
|---|---|---|
| `feat/` | 新功能 | `feat/credits` |
| `fix/` | 缺陷修复 | `fix/ttswitch-models` |
| `chore/` | 构建、清理、发布流程 | `chore/release` |

### 禁止

- 不建长期并行的开发分支（如 `develop`、`release/x.y`）。版本靠标签，不靠分支。
- 不建个人长期分支。
- 不推送临时分支到远端。远端只保留 `main`，保持仓库视图干净。

### 合并与清理

临时分支合并回 `main` 后**立即删除**：

```bash
git branch -d feat/xxx      # 小写 -d：未合并会被拒绝，安全
```

历史遗留的临时分支若确认已合入 `main`，可批量清理。清理前逐个核对合并状态：

```bash
git merge-base --is-ancestor <branch> main && echo "已合并，可删"
```

## 二、提交

### 格式

沿用 Conventional Commits 的中文变体：

```text
<类型>(<范围>): <一句话说明>
```

`<范围>` 可选，填受影响的模块。已有用法：`feat(web)`、`feat(core)`、`fix(gateway)`、
`fix(workbuddy)`。

### 类型

| 类型 | 用途 |
|---|---|
| `feat` | 新功能 |
| `fix` | 缺陷修复 |
| `docs` | 文档、变更记录 |
| `ci` | 工作流、构建脚本 |
| `chore` | 依赖、清理、无功能影响的杂项 |
| `refactor` | 重构，行为不变 |
| `test` | 仅测试 |
| `perf` | 性能优化 |

### 粒度

一个提交做一件事。不要把一个功能和一个无关修复混在一起，也不要刻意拆成
"补个分号"这种碎片提交。

**发版类的变更记录单独一个提交**（`docs: x.y.z 变更记录`），与功能提交分开，
这样发版标签指向的提交语义清晰。

### 提交前预检

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/precheck.ps1
```

覆盖 gofmt、go vet、go test、go build、变更记录脱敏、README 代号扫描、前端
typecheck 与 build，十几秒出结果。加 `-Quick` 约 5 秒（跳过 go test 与前端构建）。

唯一本机跑不了的是 `go test -race`（需要 gcc），由 CI 补齐。

## 三、版本号

遵循[语义化版本](https://semver.org/lang/zh-CN/) `MAJOR.MINOR.PATCH`：

| 变更性质 | 如何升 |
|---|---|
| 修复缺陷，不新增能力 | `PATCH`（1.0.1 → 1.0.2） |
| 新增能力，向后兼容 | `MINOR`（1.0.2 → 1.1.0） |
| 破坏性变更、不兼容的配置或接口 | `MAJOR`（1.x → 2.0.0） |

1.0.0 之后的版本由功能影响面决定，不看改动量大小。纯界面显示优化这类
"用户可见但不改接口"的改动算 `MINOR`。

## 四、变更记录

`CHANGELOG.md` 是唯一变更史来源，格式参考
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。

### 开发期

改动先写进顶部的 `## [未发布]` 段，按 `新增` / `变更` / `修复` 分类。每条写清
**改了什么、为什么改**，不写"优化体验"这类无信息量的表述。

### 发版时

把 `[未发布]` 整段改名为 `[x.y.z] - YYYY-MM-DD` 并置顶，形成独立版本段。

源码 `CHANGELOG.md` 保留完整准确的变更史；随 Release 压缩包分发的是
`scripts/sanitize_changelog.py` 生成的脱敏版（去掉内部开发代号）。脱敏脚本在
残留未脱敏内容时以非零码退出，CI 会因此失败——新增内容若命中内部代号，请改用
中性表述。

## 五、发版流程

1. 确认 `main` 已推送、本地工作区干净。
2. 把 `[未发布]` 整理为新版本段，提交 `docs: x.y.z 变更记录`。
3. 本地跑一次预检。
4. 打标签并推送：

```bash
git tag -a vx.y.z -m "x.y.z"
git push origin main
git push origin vx.y.z
```

5. 推送 `v*` 标签自动触发 Release 工作流，交叉编译 Windows / Linux amd64 产物、
   生成校验和并创建 Release。日常提交只跑 CI，不产生 Release。

### 更新签名

发布流水线会生成 `latest.json` 并用 Secret `UPDATE_SIGNING_KEY` 签名，程序据此提供一键更新。发版前确认这个 Secret 已配置：

- 私钥与 `internal/update/keys.go` 里的公钥必须是一对，流水线会用内置公钥再验一遍，不匹配则发布失败。
- 没有配置时发布仍会完成，但只有警告：该版本没有签名，客户端只提示、不允许一键安装。
- 私钥不入库。丢失私钥等于无法再给已发布的客户端签名，需要走「轮换密钥」流程，见 [UPDATE.md](UPDATE.md#发版者须知)。

### 关于等待 CI

**不主动等待或轮询远端 CI 结果。** 本地预检已覆盖除 `-race` 外的全部检查，
推送后即视为完成。若 CI 报错，由人工把错误信息贴回来再修。

例外：涉及 goroutine、共享状态、并发写等改动时，仍需看一次 `go test -race`
的结果——这一项本机跑不了。

### 重新发同一版本

Release 工作流在重复构建同标签时会先删除旧 Release（`gh release delete`），
不会草稿堆叠。但已发布出去的版本不要覆写，应升 `PATCH` 重新发。

## 六、仓库卫生

- 远端只保留 `main`，不推送临时分支。
- 已合并的本地临时分支及时删除。
- 不提交 `config.json`、`data/`、`bin/`、`web/dist/` 等本地产物，它们已在
  `.gitignore` 中。
- 提交内容做脱敏：不写真实密钥、内网地址、个人账号信息。
