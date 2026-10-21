package update

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const pageChangelog = "# 变更记录\n\n## [未发布]\n\n## [1.4.0] - 2026-10-21\n\n### 新增\n- 新功能 A\n- 新功能 B\n\n### 修复\n- 修复 C\n\n## [1.3.3] - 2026-10-20\n\n### 修复\n- 旧版本的修复 OLD\n"

func TestRenderReleasePageLeadsWithTheVersionsNotes(t *testing.T) {
	page, has := RenderReleasePage("v1.4.0", pageChangelog)
	if !has {
		t.Fatal("变更记录里有该版本小节，hasNotes 应为 true")
	}
	for _, want := range []string{"## 更新内容", "### 新增", "- 新功能 A", "- 新功能 B", "### 修复", "- 修复 C"} {
		if !strings.Contains(page, want) {
			t.Errorf("缺少 %q", want)
		}
	}
	if strings.Contains(page, "OLD") || strings.Contains(page, "## [") {
		t.Errorf("只应包含本版本小节，不应带别的版本或版本标题：\n%s", page)
	}
	// 更新内容必须排在升级方式与安装说明之前。
	order := []string{"## 更新内容", "## 如何升级", "<summary>安装与目录说明</summary>", "<summary>从旧版 workbuddy2api-panel 迁移</summary>", "## 校验"}
	last := -1
	for _, h := range order {
		i := strings.Index(page, h)
		if i < 0 {
			t.Fatalf("缺少章节 %q", h)
		}
		if i <= last {
			t.Fatalf("章节顺序不对，%q 出现得太早", h)
		}
		last = i
	}
}

// 标签带不带 v 前缀都能取到；找不到版本时给出明确说明且不失败，其余章节仍在。
func TestRenderReleasePageFallbackWhenNoSection(t *testing.T) {
	if _, has := RenderReleasePage("1.4.0", pageChangelog); !has {
		t.Error("不带 v 前缀的标签也应能取到")
	}
	page, has := RenderReleasePage("v9.9.9", pageChangelog)
	if has {
		t.Fatal("没有该版本小节时 hasNotes 应为 false")
	}
	for _, want := range []string{"本版本没有记录更新说明", "## 如何升级", "## 校验"} {
		if !strings.Contains(page, want) {
			t.Errorf("兜底页面缺少 %q", want)
		}
	}
	if strings.Contains(page, "新功能 A") {
		t.Error("兜底页面不应混入别的版本的内容")
	}
	// 变更记录为空 / 非版本标签（手动触发的分支名）同样不能 panic。
	if _, has := RenderReleasePage("main", ""); has {
		t.Error("空变更记录不应有更新内容")
	}
}

// Markdown 结构：折叠块成对出现，<summary> 之后要空一行，否则 GitHub 不会渲染折叠块里的 Markdown；
// 校验章节只出现一次（旧工作流里它被重复追加过一遍）；代码围栏成对。
func TestRenderReleasePageMarkdownStructure(t *testing.T) {
	page, _ := RenderReleasePage("v1.4.0", pageChangelog)
	if strings.Count(page, "<details>") != strings.Count(page, "</details>") || strings.Count(page, "<details>") != 2 {
		t.Errorf("折叠块应成对出现且为 2 个：%d / %d", strings.Count(page, "<details>"), strings.Count(page, "</details>"))
	}
	if strings.Count(page, "</summary>\n\n") != 2 {
		t.Error("每个 </summary> 之后都应空一行")
	}
	if strings.Count(page, "## 校验") != 1 {
		t.Errorf("校验章节应只出现一次：%d", strings.Count(page, "## 校验"))
	}
	if strings.Contains(page, "~~~") {
		t.Error("占位围栏应已全部替换成 ```")
	}
	if strings.Count(page, "```")%2 != 0 {
		t.Errorf("代码围栏应成对出现：%d", strings.Count(page, "```"))
	}
	if !strings.HasSuffix(page, "\n") || strings.Contains(page, "\r") {
		t.Error("应以单个换行结尾且只用 LF")
	}
}

// 升级说明里写的事实必须与程序行为一致，改动这些行为时要同步改这里。
func TestRenderReleasePageUpgradeFacts(t *testing.T) {
	page, _ := RenderReleasePage("v1.4.0", pageChangelog)
	for _, want := range []string{"v1.2.0 及以上", "设置 → 软件更新", "v1.1.0 及更早版本", "`admin_key`", "自动回滚", "保留你自己的 `config.json` 和 `data` 目录"} {
		if !strings.Contains(page, want) {
			t.Errorf("升级说明缺少 %q", want)
		}
	}
}

// 过长的更新内容要截断，并且不能截在汉字中间；清单用的 ReleaseNotes 仍按自己的上限。
func TestRenderReleasePageTruncatesLongNotes(t *testing.T) {
	long := "## [1.4.0] - x\n" + strings.Repeat("汉", releasePageNotesMax) + "\n\n## [1.3.3] - y\n- OLD\n"
	page, has := RenderReleasePage("v1.4.0", long)
	if !has || !utf8.ValidString(page) {
		t.Fatalf("hasNotes=%v validUTF8=%v", has, utf8.ValidString(page))
	}
	if !strings.Contains(page, "内容过长已截断") || strings.Contains(page, "OLD") {
		t.Error("应提示已截断，且不混入别的版本")
	}
	if len(page) > 100<<10 {
		t.Errorf("整页应远小于 GitHub 的 125000 字符上限：%d", len(page))
	}
	if n := ReleaseNotes(long, "1.4.0"); len(n) > maxNotesBytes {
		t.Errorf("清单用的 ReleaseNotes 仍应受 maxNotesBytes 限制：%d", len(n))
	}
}

func TestTruncateUTF8(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel"},
		{"汉字", 3, "汉"},
		{"汉字", 4, "汉"},
		{"汉字", 2, ""},
		{"", 3, ""},
	}
	for _, c := range cases {
		if got := truncateUTF8(c.in, c.max); got != c.want {
			t.Errorf("truncateUTF8(%q, %d) = %q，想要 %q", c.in, c.max, got, c.want)
		}
	}
}
