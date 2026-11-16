package weixun

import (
	"context"
	"errors"
	"time"

	"github.com/gorilla/websocket"
)

// wsDialer 是 WebSocket 拨号实现。
// 用 gorilla/websocket —— 协议端服务端本身也是用它（go.mod:15），
// 保持同一实现可以避免帧级兼容问题。
var wsDialer = dialWS

func dialWS(ctx context.Context, rawURL, key string, opts *StreamOptions, s *Stream) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		// 协议端 CheckOrigin 恒 true，不需要 Origin 头
	}

	conn, resp, err := dialer.DialContext(ctx, rawURL, nil)
	if err != nil {
		// 协议端在 key 无效时返回 HTTP 400 + {"error":"无效的key"}
		if resp != nil {
			return &APIError{
				Code:       CodeFailKey,
				Text:       "WebSocket 握手失败: " + resp.Status,
				StatusCode: resp.StatusCode,
			}
		}
		return err
	}
	defer conn.Close()

	// 协议端每 60 秒发 ping，读超时 300 秒。
	// 这里设置读超时并靠 pong 刷新，与服务端行为对齐。
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(300 * time.Second))
	})

	state(opts, "connected")

	// 独立协程在 ctx 取消时关闭连接，唤醒阻塞的 ReadMessage
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil // 主动关闭
			}
			// 协议端关闭连接（可能被新连接顶掉）
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return errors.New("WebSocket 读取失败: " + err.Error())
		}
		parseFrame(data, opts)
	}
}
