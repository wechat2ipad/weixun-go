package weixun

import (
	"encoding/json"
	"testing"
)

// TestParseFrameMessage 验证普通消息帧的解析（含 {"str":...} 解包）。
func TestParseFrameMessage(t *testing.T) {
	// 真实帧样例（来自 docs/同步消息过滤方案.md，已确认为协议端实际输出）
	frame := []byte(`{
		"key": "a1b2c3",
		"message": {
			"msg_id": 1751755490,
			"from_user_name": {"str": "50190590530@chatroom"},
			"to_user_name": {"str": "wxid_hvoabx1dxaci22"},
			"msg_type": 1,
			"content": {"str": "wxid_i43b65hm7dad41:\n12345/1"},
			"create_time": 1789611963,
			"new_msg_id": 199344895681685661
		},
		"type": "message"
	}`)

	var got *Message
	parseFrame(frame, &StreamOptions{OnMessage: func(m *Message) { got = m }})

	if got == nil {
		t.Fatal("未解析出消息")
	}
	if got.From != "50190590530@chatroom" {
		t.Errorf("From = %q（{str} 解包失败）", got.From)
	}
	if !got.IsGroup() {
		t.Error("应识别为群消息")
	}
	if got.Type != MsgTypeText {
		t.Errorf("Type = %d, want %d", got.Type, MsgTypeText)
	}
	// 关键：64 位 ID 必须完整保留
	if got.NewMsgID != "199344895681685661" {
		t.Errorf("NewMsgID = %q（大整数丢精度）", got.NewMsgID)
	}
	if got.MsgID != 1751755490 {
		t.Errorf("MsgID = %d", got.MsgID)
	}
}

// TestParseFrameRecordContent 验证 record_content 事件（无 message 包装）。
func TestParseFrameRecordContent(t *testing.T) {
	frame := []byte(`{
		"key":"k1","type":"record_content","message_id":"123456789",
		"talker":"wxid_a","success":true,"content":"<msg>...</msg>",
		"length":120,"timestamp":1789611963
	}`)
	var ev *RecordContentEvent
	parseFrame(frame, &StreamOptions{OnRecordContent: func(e *RecordContentEvent) { ev = e }})

	if ev == nil {
		t.Fatal("未解析出 record_content 事件")
	}
	if ev.MessageID != "123456789" || !ev.Success || ev.Talker != "wxid_a" {
		t.Errorf("事件字段错误: %+v", ev)
	}
}

// TestParseFramePlainText 验证裸文本控制帧不被当作 JSON 解析。
func TestParseFramePlainText(t *testing.T) {
	for _, text := range []string{"pong", "sync completed", "ping"} {
		var called bool
		parseFrame([]byte(text), &StreamOptions{
			OnMessage: func(*Message) { called = true },
			OnError:   func(error) { called = true },
		})
		if called {
			t.Errorf("%q 不应触发任何回调", text)
		}
	}
}

// TestParseFrameBareString 验证 msg_source 是裸字符串（不包在 {str} 里）。
func TestParseFrameBareString(t *testing.T) {
	frame := []byte(`{"type":"message","message":{
		"msg_type":10000,"push_content":"裸字符串内容",
		"from_user_name":{"str":"wxid_x"},"to_user_name":{"str":"wxid_y"}
	}}`)
	var got *Message
	parseFrame(frame, &StreamOptions{OnMessage: func(m *Message) { got = m }})
	if got == nil || got.PushContent != "裸字符串内容" {
		t.Errorf("裸字符串字段解析失败: %+v", got)
	}
	if !IsContactSync(got.Type) {
		t.Error("msg_type=10000 应识别为联系人同步")
	}
}

// TestUnwrapString 单元测试：两种字符串形态。
func TestUnwrapString(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"str":"abc"}`, "abc"},
		{`"abc"`, "abc"},
		{`""`, ""},
		{`null`, ""},
		{``, ""},
	}
	for _, c := range cases {
		if got := unwrapString(json.RawMessage(c.in)); got != c.want {
			t.Errorf("unwrapString(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRawInt64AsString 单元测试：数字与字符串两种大整数形态。
func TestRawInt64AsString(t *testing.T) {
	cases := []struct{ in, want string }{
		{`199344895681685660`, "199344895681685660"},
		{`"199344895681685660"`, "199344895681685660"},
		{`null`, ""},
	}
	for _, c := range cases {
		if got := rawInt64AsString(json.RawMessage(c.in)); got != c.want {
			t.Errorf("rawInt64AsString(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestStreamURL 验证 WS 地址构造（含 http→ws 转换与 key 注入）。
func TestStreamURL(t *testing.T) {
	c := NewClient("http://127.0.0.1:7006", "mykey", WithoutRateLimit())
	s := &Stream{c: c}
	u, err := s.wsURL()
	if err != nil {
		t.Fatal(err)
	}
	want := "ws://127.0.0.1:7006/ws/GetSyncMsg?key=mykey"
	if u != want {
		t.Errorf("wsURL = %q, want %q", u, want)
	}

	// https → wss
	c2 := NewClient("https://example.com", "k", WithoutRateLimit())
	s2 := &Stream{c: c2}
	u2, _ := s2.wsURL()
	if u2[:4] != "wss:" {
		t.Errorf("https 应转 wss，实际 %q", u2)
	}
}
