// Package weixun 是微寻微信协议 API 的 Go SDK。
//
// 快速开始：
//
//	c := weixun.NewClient("http://127.0.0.1:7006", "你的协议 license Key")
//	resp, err := c.Message.SendText(ctx, &weixun.SendTextRequest{
//	    To:   "wxid_xxxx",
//	    Text: "订单已发货",
//	})
//
// 设计要点（均来自协议端实测，不是猜测）：
//   - license key 一律放在 query 参数 key
//   - HTTP 状态码几乎恒为 200，成败看响应体的 Code 字段
//   - 32/64 位消息 ID 一律以字符串传输，避免 JS/PHP 精度丢失
//   - 同一 key 只允许一条 WebSocket 连接，SDK 内部加锁
package weixun

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client 是 SDK 入口。
type Client struct {
	baseURL string
	key     string
	http    *http.Client
	limiter *RateLimiter

	// 业务模块
	Message *MessageService
	Group   *GroupService
	User    *UserService
	Friend  *FriendService
	Sns     *SnsService
	Login   *LoginService
}

// Option 用于定制 Client。
type Option func(*Client)

// WithHTTPClient 替换底层 HTTP 客户端（自定义代理、超时等）。
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithTimeout 设置单次请求超时，默认 30 秒。
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.http.Timeout = d }
}

// WithoutRateLimit 关闭内置限速。
// 默认开启 40 条/分钟——这是官方风控建议，关掉请自行承担风险。
func WithoutRateLimit() Option {
	return func(c *Client) { c.limiter = nil }
}

// NewClient 创建客户端。
//
// baseURL 形如 http://127.0.0.1:7006，不要带 /api 或 /v1 前缀
// （协议端实测 apiVersion 为空；若服务端配置了版本前缀，把它一起放进 baseURL）。
func NewClient(baseURL, licenseKey string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		key:     licenseKey,
		http:    &http.Client{Timeout: 30 * time.Second},
		limiter: NewRateLimiter(40, time.Minute),
	}
	for _, opt := range opts {
		opt(c)
	}
	c.Message = &MessageService{c}
	c.Group = &GroupService{c}
	c.User = &UserService{c}
	c.Friend = &FriendService{c}
	c.Sns = &SnsService{c}
	c.Login = &LoginService{c}
	return c
}

// call 是所有业务请求的统一出口。
//
// 它做四件事：
//  1. 拼 URL（baseURL + path + ?key=）
//  2. 限速（默认 40 条/分钟，见 RateLimiter）
//  3. 解析统一信封
//  4. 按协议端真实语义判定成败并映射错误
func (c *Client) call(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}
	}

	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("weixun: 非法 baseURL: %w", err)
	}
	q := u.Query()
	q.Set("key", c.key) // 协议端唯一鉴权方式：query 参数 key
	u.RawQuery = q.Encode()

	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("weixun: 序列化请求失败: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "weixun-go/0.1.0")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("weixun: 请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("weixun: 读取响应失败: %w", err)
	}

	// 协议端在个别路径返回非 JSON（如 WS 握手失败的 {"error":"..."}）。
	// 这类情况直接按 HTTP 错误处理，不要试图解析成信封。
	if len(raw) == 0 || raw[0] != '{' {
		return &APIError{
			Code:       CodeFailGeneric,
			Text:       strings.TrimSpace(string(raw)),
			StatusCode: resp.StatusCode,
			Body:       raw,
		}
	}

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return &APIError{
			Code:       CodeFailGeneric,
			Text:       "响应不是合法 JSON: " + err.Error(),
			StatusCode: resp.StatusCode,
			Body:       raw,
		}
	}

	if !env.OK() {
		return newAPIError(&env, resp.StatusCode, raw)
	}

	if out != nil {
		if err := env.DecodeData(out); err != nil {
			return fmt.Errorf("weixun: 解析 Data 失败: %w", err)
		}
	}
	return nil
}

// Key 返回当前使用的 license key（脱敏后的形式，用于日志）。
func (c *Client) Key() string {
	if len(c.key) <= 8 {
		return "****"
	}
	return c.key[:4] + "****" + c.key[len(c.key)-4:]
}

// BaseURL 返回协议端地址。
func (c *Client) BaseURL() string { return c.baseURL }

// rawKey 仅供内部使用（例如建立 WebSocket 连接）。
func (c *Client) rawKey() string { return c.key }
