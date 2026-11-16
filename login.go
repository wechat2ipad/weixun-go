package weixun

import (
	"context"
	"fmt"
	"time"
)

// LoginService 封装扫码登录状态机（协议端 /login/* 共 19 个端点）。
//
// 状态机（实测 api/service/loginService.go 的 loginState 取值）：
//
//	waiting                 等待扫码
//	login_processing        已扫码，处理中
//	ipad_required           需要 iPad 二次确认（双扫）
//	online                  登录完成
//	security_verify_required 需要安全验证，必须重新登录
//
// 失败原因码见 reasonCode 常量（ATTEMPT_EXPIRED / WAKE_SCAN_REQUIRED 等）。
type LoginService struct {
	c *Client
}

// LoginState 是扫码登录的当前状态。
type LoginState struct {
	// State 协议端的状态数值（0/1/2/3/4/10）
	State int `json:"state"`
	// LoginState 语义化状态，见上面的状态机说明
	LoginState string `json:"loginState"`
	// NextStep 建议的下一步动作（协议端下发，如 "ipad"）
	NextStep string `json:"nextStep,omitempty"`
	// QrCodeURL 二维码内容（首次取码时返回）
	QrCodeURL string `json:"qrCodeUrl,omitempty"`
	// WxID 登录成功后的微信 ID
	WxID string `json:"wxid,omitempty"`
	// Message 提示信息
	Message string `json:"msg,omitempty"`
}

// IsOnline 判断是否已登录成功。
func (s *LoginState) IsOnline() bool { return s.LoginState == "online" }

// NeedIpadConfirm 判断是否需要 iPad 二次确认。
func (s *LoginState) NeedIpadConfirm() bool { return s.LoginState == "ipad_required" }

// NeedSecurityVerify 判断是否需要安全验证（必须重新走登录流程）。
func (s *LoginState) NeedSecurityVerify() bool {
	return s.LoginState == "security_verify_required"
}

// IsTerminal 判断流程是否已结束（成功或不可恢复失败）。
func (s *LoginState) IsTerminal() bool {
	return s.IsOnline() || s.NeedSecurityVerify()
}

// GetQRCode 获取登录二维码。
//
// 请求会带一个 attemptID：协议端要求同一 key 同时只能有一个活跃的登录尝试，
// 15 分钟 TTL。SDK 负责透传，调用方需要持久化它以便后续 CheckStatus。
func (s *LoginService) GetQRCode(ctx context.Context, device string) (*LoginState, error) {
	if device == "" {
		device = "ipad" // 默认 iPad 协议
	}
	body := map[string]interface{}{"device": device}
	var st LoginState
	if err := s.c.call(ctx, "POST", "/login/GetLoginQrCodeNewDirect", body, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// CheckStatus 查询登录进度。建议每 2.5 秒调用一次（官方推荐节奏）。
func (s *LoginService) CheckStatus(ctx context.Context, attemptID string) (*LoginState, error) {
	body := map[string]interface{}{}
	if attemptID != "" {
		body["attemptId"] = attemptID
	}
	var st LoginState
	if err := s.c.call(ctx, "POST", "/login/CheckLoginStatus", body, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// WaitForOnline 阻塞等待登录完成，内部按 2.5 秒轮询。
//
// ctx 超时或状态进入不可恢复失败（security_verify_required）时返回错误。
func (s *LoginService) WaitForOnline(ctx context.Context, attemptID string) (*LoginState, error) {
	const interval = 2500 * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		st, err := s.CheckStatus(ctx, attemptID)
		if err != nil {
			return nil, err
		}
		if st.IsOnline() {
			return st, nil
		}
		if st.NeedSecurityVerify() {
			return st, &APIError{
				Code:       CodeFailGeneric,
				Text:       "需要安全验证，必须重新发起登录",
				ReasonCode: ReasonSecurityReloginNeeded,
			}
		}
		select {
		case <-ctx.Done():
			return st, fmt.Errorf("weixun: 等待登录超时: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// Cancel 取消当前的登录尝试。
func (s *LoginService) Cancel(ctx context.Context, attemptID string) error {
	body := map[string]interface{}{}
	if attemptID != "" {
		body["attemptId"] = attemptID
	}
	return s.c.call(ctx, "POST", "/login/CancelLogin", body, nil)
}

// GetLoginStatus 查询账号当前是否在线（不需要走扫码流程）。
func (s *LoginService) GetLoginStatus(ctx context.Context, out interface{}) error {
	return s.c.call(ctx, "GET", "/login/GetLoginStatus", nil, out)
}

// WakeUp 唤醒已登录过的账号（避免重新扫码）。
func (s *LoginService) WakeUp(ctx context.Context, attemptID string) (*LoginState, error) {
	body := map[string]interface{}{}
	if attemptID != "" {
		body["attemptId"] = attemptID
	}
	var st LoginState
	if err := s.c.call(ctx, "POST", "/login/WakeUpLogin", body, &st); err != nil {
		return nil, err
	}
	return &st, nil
}
