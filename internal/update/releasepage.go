package update

import "strings"

// releasePageNotesMax Release 页面里更新内容的字节上限。GitHub 对 Release 正文的上限是 125000 个字符，
// 留足余量给下面的固定说明；正常版本的更新内容远小于这个值。
const releasePageNotesMax = 60 << 10

// RenderReleasePage 生成 GitHub Release 页面的正文（Markdown）。
//
// 页面以「这个版本改了什么」为主：先是更新内容（取自变更记录里该版本的小节），再是怎么升级，
// 安装与迁移这类只在首次安装时才需要的说明折叠在后面，不抢更新内容的位置。
//
// changelog 应当是已脱敏的发布版变更记录。hasNotes 为 false 表示变更记录里没有找到这个标签的小节
// （例如忘了写变更记录就打了标签），此时页面上会写明没有更新说明，调用方应当给出警告。
func RenderReleasePage(tag, changelog string) (page string, hasNotes bool) {
	notes := releaseSection(changelog, strings.TrimPrefix(tag, "v"))
	hasNotes = notes != ""

	var b strings.Builder
	b.WriteString("## 更新内容\n\n")
	switch {
	case !hasNotes:
		b.WriteString("本版本没有记录更新说明，完整变更见压缩包内的 `CHANGELOG.md`。\n")
	case len(notes) > releasePageNotesMax:
		b.WriteString(truncateUTF8(notes, releasePageNotesMax))
		b.WriteString("\n\n……内容过长已截断，完整变更见压缩包内的 `CHANGELOG.md`。\n")
	default:
		b.WriteString(notes)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(releaseUpgradeSection)
	return strings.ReplaceAll(b.String(), "~~~", "```"), hasNotes
}

// 发布页固定说明。代码围栏用 ~~~ 书写（Go 原始字符串里放不了反引号），输出前统一换成 ```。
// 里面的事实要与程序行为一致：软件更新功能自 v1.2.0 起提供；更新会备份并保留配置与数据、失败自动回滚；
// 一键安装要求 config.json 里设置了 admin_key；刚发布的几分钟里更新清单可能还没有刷新。
const releaseUpgradeSection = `## 如何升级

- **已安装 v1.2.0 及以上**：在面板「设置 → 软件更新」点「检查更新」，下载后点「安装并重启」。更新会先备份并保留你的配置与数据，失败时自动回滚。需要 ` + "`config.json`" + ` 里设置了 ` + "`admin_key`" + `。刚发布的几分钟内，检查更新可能还显示「已是最新」，稍等再试。
- **首次安装，或当前是 v1.1.0 及更早版本**（这些版本没有软件更新功能）：下载下方对应平台的压缩包，停止服务后用新包里的程序文件替换旧的，保留你自己的 ` + "`config.json`" + ` 和 ` + "`data`" + ` 目录，再启动。

<details>
<summary>安装与目录说明</summary>

下载对应平台的压缩包，解压即可运行。压缩包内有一层 ` + "`poolsvc-<平台>/`" + ` 目录。

**无需额外配置环境**：包内二进制为静态编译的单文件可执行程序，前端页面已内嵌，
不依赖 Go、Node.js 或任何外部运行时，也无需安装依赖。

解压后目录：

~~~text
poolsvc            主服务（Windows 为 poolsvc.exe）
migrate            旧版迁移工具（Windows 为 migrate.exe）
start.sh / start.bat   启动脚本
config.example.json    配置模板
README.md  LICENSE  CHANGELOG.md
~~~

启动：

~~~bash
cp config.example.json config.json
./poolsvc            # Windows 双击 start.bat
~~~

</details>

<details>
<summary>从旧版 workbuddy2api-panel 迁移</summary>

包内已附带迁移工具，直接运行即可，无需 Go 环境：

~~~bash
# 先预览，不写文件
./migrate -from /path/to/wb2api-panel-v1.11.7-windows-amd64 -dry-run

# 确认无误后实际执行
./migrate -from /path/to/wb2api-panel-v1.11.7-windows-amd64
~~~

迁移后重启服务生效。

</details>

## 校验

每个压缩包旁边有同名的 ` + "`.sha256`" + ` 文件。

~~~bash
# Linux
sha256sum -c poolsvc-linux-amd64.tar.gz.sha256
~~~

~~~powershell
# Windows：输出的哈希应与 .sha256 文件里的一致
Get-FileHash .\poolsvc-windows-amd64.zip -Algorithm SHA256
~~~
`
