// Command updatectl 发布侧工具：生成更新签名密钥、生成 latest.json、对其签名与验签。
// 只在发版流水线和本机使用，不随安装包分发。
//
//	updatectl keygen  -out <目录>
//	updatectl manifest -dir <产物目录> -tag vX.Y.Z -changelog <CHANGELOG.md> -out latest.json
//	updatectl sign    -in latest.json -out latest.json.sig        （私钥取自环境变量 UPDATE_SIGNING_KEY）
//	updatectl verify  -in latest.json -sig latest.json.sig -pub <base64 公钥>
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/orangeruan/poolservice/internal/update"
)

const keyEnv = "UPDATE_SIGNING_KEY"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "manifest":
		err = manifest(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "updatectl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法: updatectl keygen|manifest|sign|verify [参数]")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "", "密钥输出目录（必须在仓库之外）")
	fs.Parse(args)
	if *out == "" {
		return fmt.Errorf("需要 -out")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	keyPath := filepath.Join(*out, "update-signing.key")
	if _, err := os.Stat(keyPath); err == nil {
		return fmt.Errorf("%s 已存在，不覆盖（覆盖会让已发布版本无法验证新签名）", keyPath)
	}
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	_ = os.WriteFile(filepath.Join(*out, "update-signing.pub"), []byte(pubB64+"\n"), 0o644)
	fmt.Println("私钥已写入:", keyPath, "（不要提交到仓库；内容粘贴到 GitHub Secret UPDATE_SIGNING_KEY）")
	fmt.Println("公钥（写入 internal/update/keys.go）:", pubB64)
	return nil
}

func manifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	dir := fs.String("dir", "", "安装包所在目录")
	tag := fs.String("tag", "", "版本标签，如 v1.2.0")
	cl := fs.String("changelog", "", "用于提取更新说明的 CHANGELOG（建议用脱敏版）")
	out := fs.String("out", "latest.json", "输出文件")
	fs.Parse(args)
	var notes []byte
	if *cl != "" {
		b, err := os.ReadFile(*cl)
		if err != nil {
			return err
		}
		notes = b
	}
	b, err := update.BuildManifest(*dir, *tag, string(notes), time.Now())
	if err != nil {
		return err
	}
	return os.WriteFile(*out, b, 0o644)
}

func loadPriv() (ed25519.PrivateKey, error) {
	s := strings.TrimSpace(os.Getenv(keyEnv))
	if s == "" {
		return nil, fmt.Errorf("环境变量 %s 为空", keyEnv)
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s 不是有效的 ed25519 私钥", keyEnv)
	}
	return ed25519.PrivateKey(b), nil
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	in := fs.String("in", "", "待签名文件")
	out := fs.String("out", "", "签名输出文件")
	fs.Parse(args)
	priv, err := loadPriv()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	return os.WriteFile(*out, update.Sign(priv, raw), 0o644)
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	in := fs.String("in", "", "被签名文件")
	sig := fs.String("sig", "", "签名文件")
	pub := fs.String("pub", "", "base64 公钥；省略则使用程序内置公钥")
	fs.Parse(args)
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	sb, err := os.ReadFile(*sig)
	if err != nil {
		return err
	}
	keys := update.TrustedKeys()
	if *pub != "" {
		if keys, err = update.ParseKeys([]string{*pub}); err != nil {
			return err
		}
	}
	if !update.VerifySignature(raw, sb, keys) {
		return fmt.Errorf("签名无效")
	}
	fmt.Println("签名有效")
	return nil
}
