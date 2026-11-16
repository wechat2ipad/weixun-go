package weixun

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Envelope 是协议端所有业务接口的统一响应结构。
//
// 设计依据（来自协议端实测）：
//   - 顶层字段是 PascalCase：Code / Data / Text / Success
//   - Code 是 interface{}，可能是 int 也可能是数字字符串
//   - Data 可能是 object / array / string / null
//   - **成功判据是 Code ∈ {0,200} 优先，Success 兜底** —— 因为有反例：
//     /user/SetProxy 返回 Code=200 但 Success=false（Go 布尔零值）
//   - VerifyCode 特例返回 Code=0 + Message（字段名是 Message 不是 Text）
type Envelope struct {
	Code    json.RawMessage `json:"Code"`
	Data    json.RawMessage `json:"Data"`
	Text    string          `json:"Text"`
	Message string          `json:"Message"`
	Success *bool           `json:"Success"`

	// 协议端在登录/代理场景附带的诊断字段
	ReasonCode string `json:"reasonCode,omitempty"`
	AttemptID  string `json:"attemptId,omitempty"`
	Phase      string `json:"phase,omitempty"`
}

// CodeInt 把 Code 解析成整数。
// 协议端的 Code 可能是 200（数字）或 "200"（字符串），两种都要认。
func (e *Envelope) CodeInt() (int, bool) {
	raw := strings.TrimSpace(string(e.Code))
	if raw == "" || raw == "null" {
		return 0, false
	}
	// 数字字面量
	if n, err := strconv.Atoi(raw); err == nil {
		return n, true
	}
	// 字符串字面量
	var s string
	if err := json.Unmarshal(e.Code, &s); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// OK 按协议端的真实语义判定成功。
//
// 判定顺序（与 customer-gateway/src/protocolResponse.js 的实现一致）：
//  1. Code 存在且为 0 或 200 → 成功
//  2. Code 不存在 → 回退 Success == true
//  3. 两者都无法判定 → 失败
func (e *Envelope) OK() bool {
	if code, ok := e.CodeInt(); ok {
		return code == 0 || code == 200
	}
	return e.Success != nil && *e.Success
}

// MessageText 返回可展示的失败说明。
// 优先 Text，回退 Message（VerifyCode 接口用的是 Message）。
func (e *Envelope) MessageText() string {
	if strings.TrimSpace(e.Text) != "" {
		return e.Text
	}
	return e.Message
}

// DecodeData 把 Data 反序列化到调用方提供的目标。
func (e *Envelope) DecodeData(v interface{}) error {
	if len(e.Data) == 0 || string(e.Data) == "null" {
		return nil
	}
	return json.Unmarshal(e.Data, v)
}

// ─────────────────────────── 错误类型 ───────────────────────────

// APIError 是所有业务失败的统一错误类型。
// 调用方可以用 errors.As 取出具体字段做分支处理。
type APIError struct {
	// Code 是协议端业务码。
	//  -1 参数校验失败 / -2 key 无效或长连接不存在 / -3 账号已绑定
	//  -4 账号离线 / -5 key 不属于本服务器实例 / 300 通用业务失败
	Code int
	// Text 是协议端返回的说明，仅用于展示，不要用它做逻辑分支。
	Text string
	// ReasonCode 是登录/代理场景的结构化原因码，用于程序分支。
	// 例如 LOGIN_SESSION_NOT_FOUND / PROXY_OFFLINE / ATTEMPT_EXPIRED
	ReasonCode string
	// StatusCode 是 HTTP 状态码；协议端绝大多数情况恒为 200。
	StatusCode int
	// Body 是原始响应体，便于排查。
	Body []byte
}

func (e *APIError) Error() string {
	if e.ReasonCode != "" {
		return fmt.Sprintf("weixun: code=%d reason=%s msg=%s", e.Code, e.ReasonCode, e.Text)
	}
	return fmt.Sprintf("weixun: code=%d msg=%s", e.Code, e.Text)
}

// 协议端已知业务码（取自 api/vo/vo.go）。
const (
	CodeSuccess      = 200
	CodeLegacyOK     = 0   // VerifyCode 等老接口
	CodeFailData     = -1  // 请求体 JSON 解析失败
	CodeFailKey      = -2  // key 缺失/无效/长连接不存在
	CodeFailBound    = -3  // 账号已被绑定/已在线
	CodeFailOffline  = -4  // 账号掉线（已声明，当前无调用点）
	CodeFailNotLocal = -5  // key 不属于本服务器实例（中间件未挂载）
	CodeFailGeneric  = 300 // 通用业务失败
)

// 登录/代理相关的结构化原因码（取自 api/service/login_attempt.go 等）。
const (
	ReasonLoginSessionNotFound  = "LOGIN_SESSION_NOT_FOUND"
	ReasonProxyOffline          = "PROXY_OFFLINE"
	ReasonAttemptConflict       = "ATTEMPT_CONFLICT"
	ReasonAttemptMismatch       = "ATTEMPT_MISMATCH"
	ReasonAttemptInactive       = "ATTEMPT_INACTIVE"
	ReasonAttemptIDRequired     = "ATTEMPT_ID_REQUIRED"
	ReasonAttemptNotFound       = "ATTEMPT_NOT_FOUND"
	ReasonAttemptExpired        = "ATTEMPT_EXPIRED"
	ReasonCancelled             = "CANCELLED"
	ReasonWakeScanRequired      = "WAKE_SCAN_REQUIRED"
	ReasonSecurityReloginNeeded = "SECURITY_RELOGIN_REQUIRED"
	ReasonSessionInvalid        = "SESSION_INVALID"
	ReasonWakeRequestFailed     = "WAKE_REQUEST_FAILED"
	ReasonLoginRequestFailed    = "LOGIN_REQUEST_FAILED"
)

// IsKeyInvalid 判断是否为 key 无效类错误，调用方据此提示重新登录。
func (e *APIError) IsKeyInvalid() bool {
	return e.Code == CodeFailKey || e.ReasonCode == ReasonLoginSessionNotFound
}

// IsRateLimited 判断是否疑似被风控限流。
// 协议端本身不返回 429（无服务端限流），这里主要匹配业务文案。
func (e *APIError) IsRateLimited() bool {
	return e.StatusCode == 429 ||
		strings.Contains(e.Text, "频繁") ||
		strings.Contains(e.Text, "限制") ||
		strings.Contains(e.Text, "frequency")
}

// newAPIError 由响应体构造错误。
func newAPIError(env *Envelope, statusCode int, body []byte) *APIError {
	code, ok := env.CodeInt()
	if !ok {
		code = CodeFailGeneric
	}
	text := env.MessageText()
	if text == "" && statusCode != 200 {
		text = fmt.Sprintf("HTTP %d", statusCode)
	}
	return &APIError{
		Code:       code,
		Text:       text,
		ReasonCode: env.ReasonCode,
		StatusCode: statusCode,
		Body:       body,
	}
}
