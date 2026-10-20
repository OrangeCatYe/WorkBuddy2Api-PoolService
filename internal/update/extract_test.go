package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractZipWhitelistAndStripTopDir(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "p.zip")
	os.WriteFile(zp, zipBytes(t, map[string]string{
		"poolsvc-windows-amd64/poolsvc.exe":            "EXE",
		"poolsvc-windows-amd64/migrate.exe":            "MIG",
		"poolsvc-windows-amd64/README.md":              "readme",
		"poolsvc-windows-amd64/start.bat":              "bat",
		"poolsvc-windows-amd64/autostart/windows.ps1":  "ps1",
		"poolsvc-windows-amd64/config.json":            "绝不能落地的用户配置",
		"poolsvc-windows-amd64/data/state.json":        "绝不能落地",
		"poolsvc-windows-amd64/unknown-future-file.md": "未知文件忽略",
		"stray.txt": "不在顶层目录里的零散文件",
	}), 0o644)

	dest := filepath.Join(dir, "stage")
	files, err := Extract(zp, dest, "windows")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"poolsvc.exe": "EXE", "migrate.exe": "MIG", "README.md": "readme", "start.bat": "bat", "autostart/windows.ps1": "ps1"}
	if len(files) != len(want) {
		t.Fatalf("只应落地白名单文件，实际: %v", files)
	}
	for rel, body := range want {
		if got := readFile(t, filepath.Join(dest, filepath.FromSlash(rel))); got != body {
			t.Errorf("%s 内容不对: %q", rel, got)
		}
	}
	for _, rel := range []string{"config.json", "data", "unknown-future-file.md", "stray.txt"} {
		if exists(filepath.Join(dest, rel)) {
			t.Errorf("%s 不应被解压出来", rel)
		}
	}
}

func TestExtractRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{
		"poolsvc-windows-amd64/../../evil.exe",
		"poolsvc-windows-amd64/../poolsvc.exe",
		"/abs/poolsvc.exe",
		"C:/evil/poolsvc.exe",
		"poolsvc-windows-amd64\\..\\..\\evil.exe",
	} {
		dir := t.TempDir()
		zp := filepath.Join(dir, "p.zip")
		os.WriteFile(zp, zipBytes(t, map[string]string{"poolsvc-windows-amd64/poolsvc.exe": "EXE", name: "X"}), 0o644)
		dest := filepath.Join(dir, "stage")
		if _, err := Extract(zp, dest, "windows"); err == nil {
			t.Errorf("%q 应被拒绝", name)
		}
		if exists(dest) {
			t.Errorf("%q：拒绝后不应留下半成品目录", name)
		}
		if exists(filepath.Join(dir, "evil.exe")) || exists(filepath.Join(filepath.Dir(dir), "evil.exe")) {
			t.Errorf("%q：文件被写到了目录外", name)
		}
	}
}

func TestExtractTarGzIgnoresLinks(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "p.tar.gz")
	os.WriteFile(tp, tgzBytes(t, []tarEntry{
		{name: "poolsvc-linux-amd64/", typ: 53}, // 目录
		{name: "poolsvc-linux-amd64/poolsvc", body: "ELF"},
		{name: "poolsvc-linux-amd64/start.sh", body: "sh"},
		{name: "poolsvc-linux-amd64/README.md", typ: 50, link: "/etc/passwd"}, // 符号链接：忽略
		{name: "poolsvc-linux-amd64/LICENSE", typ: 49, link: "poolsvc"},       // 硬链接：忽略
		{name: "poolsvc-linux-amd64/autostart/poolsvc.service", body: "unit"},
	}), 0o644)
	dest := filepath.Join(dir, "stage")
	files, err := Extract(tp, dest, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("应只落地 3 个普通文件: %v", files)
	}
	if exists(filepath.Join(dest, "README.md")) || exists(filepath.Join(dest, "LICENSE")) {
		t.Error("链接条目不应被还原成文件")
	}
}

func TestExtractRequiresMainBinary(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "p.zip")
	os.WriteFile(zp, zipBytes(t, map[string]string{"poolsvc-windows-amd64/README.md": "x"}), 0o644)
	if _, err := Extract(zp, filepath.Join(dir, "stage"), "windows"); err == nil || !strings.Contains(err.Error(), "主程序") {
		t.Fatalf("没有主程序必须拒绝: %v", err)
	}
	if _, err := Extract(filepath.Join(dir, "p.rar"), filepath.Join(dir, "s2"), "windows"); err == nil {
		t.Fatal("不支持的格式必须拒绝")
	}
}

func TestExtractRejectsOversizeAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	// 声明为小文件但实际远超上限（压缩炸弹类）：必须按实际读取量拦截。
	big := strings.Repeat("A", maxFileBytes+10)
	zp := filepath.Join(dir, "big.zip")
	os.WriteFile(zp, zipBytes(t, map[string]string{"poolsvc-windows-amd64/poolsvc.exe": big}), 0o644)
	if _, err := Extract(zp, filepath.Join(dir, "s1"), "windows"); err == nil {
		t.Error("超过单文件上限必须拒绝")
	}
	bad := filepath.Join(dir, "bad.zip")
	os.WriteFile(bad, []byte("this is not a zip"), 0o644)
	if _, err := Extract(bad, filepath.Join(dir, "s2"), "windows"); err == nil {
		t.Error("损坏的安装包必须拒绝")
	}
	badTgz := filepath.Join(dir, "bad.tar.gz")
	os.WriteFile(badTgz, []byte("this is not gzip"), 0o644)
	if _, err := Extract(badTgz, filepath.Join(dir, "s3"), "linux"); err == nil {
		t.Error("损坏的 tar.gz 必须拒绝")
	}
}

func TestPackageRulesNeverTouchUserData(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		for rel := range PackageRules(goos) {
			low := strings.ToLower(rel)
			if strings.Contains(low, "config.json") || strings.HasPrefix(low, "data") || strings.Contains(low, "auths") || strings.Contains(low, "state") {
				t.Errorf("%s 的白名单含有用户数据路径: %s", goos, rel)
			}
		}
	}
	if PackageRules("windows")["start.bat"] != ModeIfMissing || PackageRules("linux")["start.sh"] != ModeIfMissing {
		t.Error("启动脚本用户可能改过，只能在缺失时补")
	}
}
