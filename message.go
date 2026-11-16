package weixun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MessageService 封装消息相关接口（协议端 /message/* 共 35 个端点）。
type MessageService struct {
	c *Client
}

// SendTextRequest 发送文本消息的请求。
//
// 注意协议端的真实契约：请求体是 {"MsgItem": [ {...} ]}，
// MsgItem 是**数组**而不是对象（官网旧文档这里写错了）。
// 本 SDK 把它包装成更符合直觉的单条/多条两种入参。
type SendTextRequest struct {
	// To 接收方 wxid（个人）或 xxx@chatroom（群）
	To string
	// Text 消息内容
	Text string
	// At 需要 @ 的成员 wxid 列表，可选
	At []string
}

// SendTextResult 是单条发送的结果。
//
// 协议端对批量发送返回数组，每条的字段名不统一：
//   - toUSerName（大写 S，14 处）与 toUserName（9 处）混用
//   - 成功标志有 isSendSuccess / isCommentSuccess / isMarkSuccess 等多种命名
//
// SDK 把这些差异归一化成 OK + To + ErrorMessage 三个字段。
type SendTextResult struct {
	// OK 该条是否发送成功
	OK bool
	// To 接收方
	To string
	// ErrorMessage 失败原因（成功时为空）
	ErrorMessage string
	// Raw 协议端原始返回，需要细节时自行解析
	Raw json.RawMessage
}

// SendText 发送一条文本消息。
func (s *MessageService) SendText(ctx context.Context, req *SendTextRequest) (*SendTextResult, error) {
	results, err := s.SendTextBatch(ctx, []*SendTextRequest{req})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, &APIError{Code: CodeFailGeneric, Text: "协议端未返回发送结果"}
	}
	return results[0], nil
}

// SendTextBatch 批量发送文本消息。
//
// 协议端内部会在多条之间随机 sleep 1~3 秒（api/service/messageService.go:868），
// SDK 不再重复节流，但整体仍受 RateLimiter 约束。
func (s *MessageService) SendTextBatch(ctx context.Context, reqs []*SendTextRequest) ([]*SendTextResult, error) {
	if len(reqs) == 0 {
		return nil, fmt.Errorf("weixun: 至少需要一条消息")
	}
	items := make([]map[string]interface{}, 0, len(reqs))
	for _, r := range reqs {
		if r == nil || r.To == "" {
			return nil, fmt.Errorf("weixun: 接收方不能为空")
		}
		item := map[string]interface{}{
			"ToUserName":  r.To,
			"TextContent": r.Text,
			"MsgType":     1, // 1 = 文本
		}
		if len(r.At) > 0 {
			item["AtWxIDList"] = r.At
		}
		items = append(items, item)
	}
	body := map[string]interface{}{"MsgItem": items}

	var raw json.RawMessage
	if err := s.c.call(ctx, "POST", "/message/SendTextMessage", body, &raw); err != nil {
		return nil, err
	}
	return parseBatchResults(raw), nil
}

// SendImage 发送图片（base64）。
// 协议端同时接受纯 base64 和 data URI 前缀（data:image/png;base64,...）。
func (s *MessageService) SendImage(ctx context.Context, to, imageBase64 string) (*SendTextResult, error) {
	body := map[string]interface{}{
		"MsgItem": []map[string]interface{}{{
			"ToUserName":   to,
			"ImageContent": imageBase64,
			"MsgType":      2, // 2 = 图片
		}},
	}
	var raw json.RawMessage
	if err := s.c.call(ctx, "POST", "/message/SendImageMessage", body, &raw); err != nil {
		return nil, err
	}
	results := parseBatchResults(raw)
	if len(results) == 0 {
		return nil, &APIError{Code: CodeFailGeneric, Text: "协议端未返回发送结果"}
	}
	return results[0], nil
}

// GroupMassText 向多个群/联系人批量发送相同文本。
//
// 注意：协议端的 ToUserName 是**数组**，且返回的是微信 MassSendResponse
// 原样透传，**没有**收件人级别的成功/失败明细，只能看顶层 baseResponse.ret。
func (s *MessageService) GroupMassText(ctx context.Context, to []string, content string) (json.RawMessage, error) {
	if len(to) == 0 {
		return nil, fmt.Errorf("weixun: 收件人列表不能为空")
	}
	body := map[string]interface{}{
		"ToUserName": to,
		"Content":    content,
	}
	var raw json.RawMessage
	if err := s.c.call(ctx, "POST", "/message/GroupMassMsgText", body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Revoke 撤回消息。newMsgId 必须是协议端返回的字符串形式 ID。
func (s *MessageService) Revoke(ctx context.Context, to, newMsgID, clientMsgID string) error {
	body := map[string]interface{}{
		"ToUserName":  to,
		"NewMsgId":    newMsgID, // 字符串，避免 64 位精度丢失
		"ClientMsgId": clientMsgID,
	}
	return s.c.call(ctx, "POST", "/message/RevokeMsg", body, nil)
}

// MarkAllRead 标记会话已读。
func (s *MessageService) MarkAllRead(ctx context.Context, to string) error {
	body := map[string]interface{}{"ToUserName": to}
	return s.c.call(ctx, "POST", "/message/MarkAllMessagesAsRead", body, nil)
}

// GetCallback 查询当前账号的消息回调配置。
func (s *MessageService) GetCallback(ctx context.Context, out interface{}) error {
	return s.c.call(ctx, "GET", "/message/GetCallback", nil, out)
}

// SetCallback 设置 HTTP 消息回调地址。
// 协议端会在收到消息时 POST 到该地址，5 秒超时、不重试。
func (s *MessageService) SetCallback(ctx context.Context, callbackURL string) error {
	body := map[string]interface{}{"CallbackUrl": callbackURL}
	return s.c.call(ctx, "POST", "/message/SetCallback", body, nil)
}

// ─────────────────── 逐条结果归一化 ───────────────────

// parseBatchResults 把协议端五花八门的逐条结果统一成 SendTextResult。
//
// 实测的字段差异（全部来自 api/service/*.go）：
//   - 名称：toUSerName（14处）/ toUserName（9处）
//   - 成功标志：isSendSuccess / isCommentSuccess / isMarkSuccess /
//     isHideSuccess / isFollowSuccess / isUploadSuccess / isSuccess
//   - 错误信息：errMsg / errMgs（拼写错误，UploadFriendCircleImage 用）
func parseBatchResults(raw json.RawMessage) []*SendTextResult {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	// 单对象 或 数组
	var arr []map[string]interface{}
	if err := json.Unmarshal(raw, &arr); err != nil {
		var one map[string]interface{}
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil
		}
		arr = []map[string]interface{}{one}
	}

	out := make([]*SendTextResult, 0, len(arr))
	for _, item := range arr {
		res := &SendTextResult{
			To:  pickString(item, "toUSerName", "toUserName", "to_user_name"),
			Raw: mustMarshal(item),
		}
		res.OK = pickBool(item,
			"isSendSuccess", "isCommentSuccess", "isMarkSuccess",
			"isHideSuccess", "isFollowSuccess", "isUploadSuccess", "isSuccess")
		// 没有明确成功标志时，看是否有错误信息
		if res.ErrorMessage = pickString(item, "errMsg", "errMgs", "error"); res.ErrorMessage == "" && !hasAnyFlag(item) {
			res.OK = true // 无标志且无错误 → 视为成功
		}
		if res.ErrorMessage != "" {
			res.OK = false
		}
		out = append(out, res)
	}
	return out
}

func hasAnyFlag(item map[string]interface{}) bool {
	for _, k := range []string{
		"isSendSuccess", "isCommentSuccess", "isMarkSuccess", "isHideSuccess",
		"isFollowSuccess", "isUploadSuccess", "isSuccess",
	} {
		if _, ok := item[k]; ok {
			return true
		}
	}
	return false
}

func pickString(item map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := item[k]; ok && v != nil {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func pickBool(item map[string]interface{}, keys ...string) bool {
	for _, k := range keys {
		if v, ok := item[k]; ok {
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return false
}

func mustMarshal(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// ─────────────────── 消息类型常量 ───────────────────

// 协议端 msg_type 取值（clientsdk/baseinfo/enums.go + 控制台映射表）。
const (
	MsgTypeText     = 1
	MsgTypeImage    = 3
	MsgTypeVoice    = 34
	MsgTypeCard     = 42
	MsgTypeVideo    = 43
	MsgTypeSticker  = 47
	MsgTypeLocation = 48
	MsgTypeApp      = 49
	MsgTypeSystem   = 10000 // 含联系人同步事件
	MsgTypeRevoke   = 10002
)

// IsContactSync 判断是否为联系人同步事件。
// 协议端把联系人事件塞进伪造成 msg_type=10000 的消息里，
// 且它的 new_msg_id 是无意义的伪造值（time.Now().UnixNano()），不要用它做去重。
func IsContactSync(msgType int) bool { return msgType == MsgTypeSystem }

// IsRevoke 判断是否为撤回通知。
func IsRevoke(msgType int) bool { return msgType == MsgTypeRevoke }

// 确保 time 被使用（预留：后续 HttpsyncMsg 轮询会用到）
var _ = time.Now
var _ = strings.TrimSpace
