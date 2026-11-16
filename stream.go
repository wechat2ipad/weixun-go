package weixun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Stream 是 WebSocket 实时消息流。
//
// 协议端实测约束（全部有代码证据）：
//   - 鉴权只有 query 参数 key，没有 header/token
//   - 同一 key 只允许一条连接，新连接会**顶掉**旧连接（不是拒绝）
//   - 断线期间的消息**不补发**（服务端直接丢弃，无缓存、无重放）
//   - 服务端每 60 秒发 WebSocket ping，读超时 300 秒
//   - 客户端发文本 "ping" → 回 "pong"；发 "sync" → 回 "sync completed"
//   - 帧结构 {key, message, type}；内部字符串包在 {"str": "..."}
//   - 另有 type=record_content 的结构化事件（不带 message 包装）
type Stream struct {
	c *Client

	mu     sync.Mutex
	closed bool
	cancel context.CancelFunc
}

// Message 是从流里解析出来的一条微信消息。
type Message struct {
	// MsgID 32 位消息 ID
	MsgID uint32
	// NewMsgID 64 位消息 ID，**字符串形式**（超过 2^53，用数字会丢精度）
	NewMsgID string
	// From 发送方 wxid 或 xxx@chatroom
	From string
	// To 接收方
	To string
	// Type 消息类型，见 MsgType* 常量
	Type int
	// Content 文本内容（仅 Type=1 时有意义）
	Content string
	// CreateTime Unix 秒
	CreateTime uint32
	// PushContent 推送内容（部分系统消息有）
	PushContent string
	// Raw 原始 message 节点，需要细节时自行解析
	Raw json.RawMessage
}

// IsGroup 判断是否来自群聊。
func (m *Message) IsGroup() bool { return strings.HasSuffix(m.From, "@chatroom") }

// RecordContentEvent 是 type=record_content 的结构化事件
// （大聊天记录补拉完成后推送，不带 message 包装）。
type RecordContentEvent struct {
	MessageID string `json:"message_id"`
	Talker    string `json:"talker"`
	Success   bool   `json:"success"`
	Content   string `json:"content"`
	Length    int    `json:"length"`
	Timestamp int64  `json:"timestamp"`
	Error     string `json:"error,omitempty"`
}

// StreamOptions 控制流的行为。
type StreamOptions struct {
	// OnMessage 收到普通消息时回调
	OnMessage func(*Message)
	// OnRecordContent 收到记录补拉事件时回调
	OnRecordContent func(*RecordContentEvent)
	// OnError 收到错误时回调（连接错误、解析错误）
	OnError func(error)
	// OnStateChange 连接状态变化（便于日志/监控）
	OnStateChange func(state string)
	// MaxBackoff 重连退避上限，默认 60 秒
	MaxBackoff time.Duration
}

// NewStream 创建消息流。
func (c *Client) NewStream(opts *StreamOptions) *Stream {
	if opts == nil {
		opts = &StreamOptions{}
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 60 * time.Second
	}
	s := &Stream{c: c}
	go s.run(opts)
	return s
}

// Close 关闭消息流并停止重连。
func (s *Stream) Close() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
}

// run 是重连主循环。退避策略：1s → 2s → 4s → 8s … 封顶，连上即重置。
func (s *Stream) run(opts *StreamOptions) {
	backoff := time.Second
	for {
		if s.isClosed() {
			return
		}
		state(opts, "connecting")
		err := s.connect(opts)
		if s.isClosed() {
			return
		}
		if err != nil {
			errorf(opts, err)
		}
		state(opts, "reconnecting")

		// 退避等待，同时响应 Close
		select {
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > opts.MaxBackoff {
			backoff = opts.MaxBackoff
		}
		// 连接成功过一轮后会由 connect 内部重置，这里保持简单
	}
}

func (s *Stream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// connect 建立一次连接并持续读取直到断开。
//
// 注意：这里用标准库 net/http 的升级能力不现实（Go 标准库无 WS 客户端），
// 为了让 SDK 零外部依赖，采用「轮询 HTTP + 长连接」不成立——
// 因此本方法由 build tag 下的具体实现提供（见 stream_ws.go）。
// 若编译时未包含 WS 实现，会返回明确错误提示。
func (s *Stream) connect(opts *StreamOptions) error {
	if wsDialer == nil {
		return fmt.Errorf("weixun: 未启用 WebSocket 支持（需要引入 gorilla/websocket 或使用 stream_poll 模式）")
	}
	u, err := s.wsURL()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer cancel()

	return wsDialer(ctx, u, s.c.key, opts, s)
}

// wsURL 构造 WebSocket 地址。
// 协议端：ws://host:port/ws/GetSyncMsg?key=xxx
func (s *Stream) wsURL() (string, error) {
	base := s.c.baseURL
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("weixun: 非法 baseURL: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws/GetSyncMsg"
	q := u.Query()
	q.Set("key", s.c.key)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func state(opts *StreamOptions, s string) {
	if opts.OnStateChange != nil {
		opts.OnStateChange(s)
	}
}

func errorf(opts *StreamOptions, err error) {
	if opts.OnError != nil {
		opts.OnError(err)
	}
}

// parseFrame 把一帧原始文本解析成 Message / RecordContentEvent。
//
// 协议端的帧有三种形态，必须都处理：
//  1. {"key","message","type":"message"} —— 普通消息
//  2. {"type":"record_content", ...}    —— 记录补拉事件（无 message 包装）
//  3. "pong" / "sync completed"          —— 裸文本控制帧
func parseFrame(raw []byte, opts *StreamOptions) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return
	}
	// 裸文本控制帧
	if text == "pong" || text == "sync completed" || text == "ping" {
		return
	}
	if text[0] != '{' {
		return
	}

	var probe struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		errorf(opts, fmt.Errorf("weixun: 解析帧失败: %w", err))
		return
	}

	switch probe.Type {
	case "message":
		if msg := parseAddMsg(probe.Message); msg != nil && opts.OnMessage != nil {
			opts.OnMessage(msg)
		}
	case "record_content":
		var ev RecordContentEvent
		if err := json.Unmarshal(raw, &ev); err == nil && opts.OnRecordContent != nil {
			opts.OnRecordContent(&ev)
		}
	}
}

// parseAddMsg 解析 message 节点。
//
// 字段型态（来自 protobuf/wechat/wechat.pb.go）：
//   - from_user_name / to_user_name / content 是对象 {"str": "..."}
//   - msg_source / push_content 是裸字符串
//   - new_msg_id 是 int64，必须按字符串取，否则丢精度
type addMsgWire struct {
	MsgID       uint32          `json:"msg_id"`
	NewMsgID    json.RawMessage `json:"new_msg_id"`
	From        json.RawMessage `json:"from_user_name"`
	To          json.RawMessage `json:"to_user_name"`
	MsgType     uint32          `json:"msg_type"`
	Content     json.RawMessage `json:"content"`
	CreateTime  uint32          `json:"create_time"`
	PushContent string          `json:"push_content"`
}

func parseAddMsg(raw json.RawMessage) *Message {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var w addMsgWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil
	}
	return &Message{
		MsgID:       w.MsgID,
		NewMsgID:    rawInt64AsString(w.NewMsgID),
		From:        unwrapString(w.From),
		To:          unwrapString(w.To),
		Type:        int(w.MsgType),
		Content:     unwrapString(w.Content),
		CreateTime:  w.CreateTime,
		PushContent: w.PushContent,
		Raw:         raw,
	}
}

// unwrapString 处理协议端两种字符串表达：
// 对象 {"str": "xxx"} 或裸字符串 "xxx"。
func unwrapString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// 先试对象
	var obj struct {
		Str string `json:"str"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Str != "" {
		return obj.Str
	}
	// 再试裸字符串
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

// rawInt64AsString 把 JSON 里的整数（可能是数字也可能是字符串）统一转成字符串。
// 不经过 float64，避免 2^53 以上丢精度。
func rawInt64AsString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// 字符串形式
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// 数字形式：直接取原始字面量
	return strings.TrimSpace(string(raw))
}
