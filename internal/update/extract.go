package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// 安装包解压的硬性上限，防止压缩炸弹。
const (
	maxFileBytes  = 100 << 20
	maxTotalBytes = 300 << 20
)

// 文件写入策略。
const (
	ModeReplace   = "replace"    // 覆盖
	ModeIfMissing = "if_missing" // 仅当目标不存在时放入（用户可能改过，如启动脚本）
)

// ExeName 给可执行文件名加平台后缀。
func ExeName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// PackageRules 安装包里允许落地的文件（相对包根目录，用 / 分隔）及写入策略。
// 白名单之外的条目一律忽略，包里将来新增的文件不会写到用户目录里。
// 绝不包含 config.json、data/、auths/ 等用户数据。
func PackageRules(goos string) map[string]string {
	exe := func(b string) string {
		if goos == "windows" {
			return b + ".exe"
		}
		return b
	}
	rules := map[string]string{
		exe("poolsvc"):        ModeReplace,
		exe("migrate"):        ModeReplace,
		"README.md":           ModeReplace,
		"LICENSE":             ModeReplace,
		"CHANGELOG.md":        ModeReplace,
		"config.example.json": ModeReplace,
	}
	if goos == "windows" {
		rules["start.bat"] = ModeIfMissing
		rules["autostart/windows.ps1"] = ModeReplace
	} else {
		rules["start.sh"] = ModeIfMissing
		rules["autostart/poolsvc.service"] = ModeReplace
	}
	return rules
}

// Extract 把安装包解压到 dest，只落地白名单文件，返回实际写入的相对路径。
// 安装包应有唯一的顶层目录（poolsvc-<os>-<arch>/），解压时剥掉。
func Extract(archive, dest, goos string) ([]string, error) {
	rules := PackageRules(goos)
	if err := os.RemoveAll(dest); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	var (
		written []string
		total   int64
		ex      = &extractor{dest: dest, rules: rules, total: &total}
	)
	var err error
	switch {
	case strings.HasSuffix(archive, ".zip"):
		err = ex.zip(archive)
	case strings.HasSuffix(archive, ".tar.gz"):
		err = ex.tgz(archive)
	default:
		err = fmt.Errorf("不支持的安装包格式: %s", filepath.Base(archive))
	}
	if err != nil {
		_ = os.RemoveAll(dest)
		return nil, err
	}
	written = ex.written
	if _, ok := find(written, ExeNameFor(goos, "poolsvc")); !ok {
		_ = os.RemoveAll(dest)
		return nil, errors.New("安装包里没有主程序，已放弃")
	}
	return written, nil
}

// ExeNameFor 按指定系统给文件名加后缀。
func ExeNameFor(goos, base string) string {
	if goos == "windows" {
		return base + ".exe"
	}
	return base
}

func find(list []string, want string) (int, bool) {
	for i, s := range list {
		if s == want {
			return i, true
		}
	}
	return -1, false
}

type extractor struct {
	dest    string
	rules   map[string]string
	total   *int64
	written []string
}

// rel 把包内路径规整为相对包根的路径；非法路径返回错误，不在白名单返回 ("", nil)。
func (e *extractor) rel(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", fmt.Errorf("安装包含有非法路径: %q", name)
	}
	parts := strings.Split(strings.Trim(name, "/"), "/")
	for _, p := range parts {
		if p == ".." {
			return "", fmt.Errorf("安装包含有非法路径: %q", name)
		}
	}
	if len(parts) < 2 { // 顶层目录本身，或不在顶层目录里的零散文件
		return "", nil
	}
	r := path.Join(parts[1:]...)
	if _, ok := e.rules[r]; !ok {
		return "", nil
	}
	return r, nil
}

func (e *extractor) save(rel string, size int64, r io.Reader) error {
	if size > maxFileBytes {
		return fmt.Errorf("安装包中的 %s 过大", rel)
	}
	target := filepath.Join(e.dest, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if strings.HasSuffix(rel, ".exe") || strings.HasSuffix(rel, ".sh") || rel == "poolsvc" || rel == "migrate" {
		perm = 0o755
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	// 不信任头部声明的大小：实际读取时再卡一次上限。
	n, err := io.Copy(f, io.LimitReader(r, maxFileBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > maxFileBytes {
		return fmt.Errorf("安装包中的 %s 过大", rel)
	}
	*e.total += n
	if *e.total > maxTotalBytes {
		return errors.New("安装包解压后体积异常，已放弃")
	}
	e.written = append(e.written, rel)
	return nil
}

func (e *extractor) zip(file string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return fmt.Errorf("安装包无法打开: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		rel, err := e.rel(f.Name)
		if err != nil {
			return err
		}
		if rel == "" || f.FileInfo().IsDir() || !f.Mode().IsRegular() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = e.save(rel, int64(f.UncompressedSize64), rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *extractor) tgz(file string) error {
	fh, err := os.Open(file)
	if err != nil {
		return err
	}
	defer fh.Close()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		return fmt.Errorf("安装包无法打开: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("安装包损坏: %w", err)
		}
		rel, err := e.rel(h.Name)
		if err != nil {
			return err
		}
		if rel == "" || h.Typeflag != tar.TypeReg {
			continue // 目录、符号链接、硬链接一律忽略
		}
		if err := e.save(rel, h.Size, tr); err != nil {
			return err
		}
	}
}
