package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	userAgent      = "poolsvc-updater"
	idleTimeout    = 60 * time.Second // 下载时连续这么久没有收到数据就放弃
	requestTimeout = 25 * time.Second // 拉取清单 / 签名的整体超时
)

// NewHTTPClient 构造访问下载源的客户端。proxy 为空时跟随系统环境变量（HTTP_PROXY 等）。
// 重定向只能落在 https（回环地址除外），避免被诱导降级到明文。
func NewHTTPClient(proxy string) (*http.Client, error) {
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	if proxy = strings.TrimSpace(proxy); proxy != "" {
		pu, err := ParseProxy(proxy)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(pu)
	} else {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return errors.New("重定向次数过多")
			}
			return ValidateURL(req.URL.String())
		},
	}, nil
}

// ParseProxy 解析代理地址，支持 http / https / socks5 / socks5h。
func ParseProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("代理地址无效: %q", MaskProxy(raw))
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return u, nil
	}
	return nil, errors.New("代理只支持 http、https、socks5")
}

// MaskProxy 隐藏代理地址中的密码，用于接口返回与日志。
func MaskProxy(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, has := u.User.Password(); !has {
		return raw
	}
	// 不用 url.UserPassword(…, "***")：它会把 * 转义成 %2A，用户看到会困惑。
	masked := *u
	masked.User = url.User(u.User.Username())
	prefix := masked.Scheme + "://" + masked.User.String()
	return strings.Replace(masked.String(), prefix, prefix+":***", 1)
}

// fetchSmall 拉取小文件（清单 / 签名）。返回 (内容, HTTP 状态码)；状态码 >= 400 时内容为空且不报错，
// 由调用方决定 404 的含义（例如没有签名文件）。
func fetchSmall(ctx context.Context, c *http.Client, rawURL string, limit int64) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, resp.StatusCode, nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if int64(len(b)) > limit {
		return nil, resp.StatusCode, errors.New("响应内容过大")
	}
	return b, resp.StatusCode, nil
}

// idleReader 每次读到数据就重置空闲计时器，长时间没有数据则取消请求。
type idleReader struct {
	r     io.Reader
	timer *time.Timer
	idle  time.Duration
}

func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.timer.Reset(i.idle)
	}
	return n, err
}

// Download 下载到 dst，边下边算 sha256，并严格核对声明的大小与哈希；通过后才改名为 dst，
// 校验失败或中断时不会留下看起来完整的文件。
func Download(ctx context.Context, c *http.Client, rawURL, dst string, size int64, wantSHA string, progress func(done int64)) error {
	return download(ctx, c, rawURL, dst, size, wantSHA, idleTimeout, progress)
}

func download(ctx context.Context, c *http.Client, rawURL, dst string, size int64, wantSHA string, idle time.Duration, progress func(done int64)) error {
	if size <= 0 || size > MaxAssetBytes {
		return fmt.Errorf("安装包大小无效: %d", size)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	part := dst + ".part"
	_ = os.Remove(part)
	_ = os.Remove(dst)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := time.AfterFunc(idle, cancel)
	defer timer.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("连接下载源失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败：服务器返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > 0 && resp.ContentLength != size {
		return fmt.Errorf("下载失败：文件大小 %d 与清单声明的 %d 不一致", resp.ContentLength, size)
	}

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			_ = os.Remove(part)
		}
	}()

	h := sha256.New()
	var done int64
	buf := make([]byte, 64<<10)
	body := &idleReader{r: resp.Body, timer: timer, idle: idle}
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > size {
				return fmt.Errorf("下载失败：内容超过清单声明的大小 %d", size)
			}
			h.Write(buf[:n])
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			if progress != nil {
				progress(done)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return errors.New("下载中断：长时间没有收到数据")
			}
			return fmt.Errorf("下载中断: %w", rerr)
		}
	}
	if done != size {
		return fmt.Errorf("下载不完整：收到 %d 字节，应为 %d", done, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		return fmt.Errorf("校验失败：安装包 sha256 与清单不一致（下载源可能被篡改或文件已损坏）")
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(part, dst); err != nil {
		return err
	}
	ok = true
	return nil
}
