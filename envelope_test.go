package weixun

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestEnvelopeOK 覆盖协议端所有真实成功/失败形态。
func TestEnvelopeOK(t *testing.T) {
	cases := []struct {
		name string
		json string
		want bool
	}{
		// 标准成功
		{"code-200", `{"Code":200,"Data":{},"Success":true}`, true},
		// Code 是数字字符串（协议端某些路径会这样返回）
		{"code-string-200", `{"Code":"200","Data":{},"Success":true}`, true},
		// 老接口用 Code=0
		{"code-0", `{"Code":0,"Success":true,"Message":"验证成功"}`, true},
		// 反例：SetProxy 返回 Code=200 但 Success=false（Go 布尔零值没填）
		{"code-200-success-false", `{"Code":200,"Data":{}}`, true},
		// 只有 Success，没有 Code
		{"success-only", `{"Success":true}`, true},
		// 通用业务失败
		{"code-300", `{"Code":300,"Text":"业务失败","Success":false}`, false},
		// key 无效
		{"code-minus-2", `{"Code":-2,"Text":" 该链接不存在！","Success":false}`, false},
		// 参数错误
		{"code-minus-1", `{"Code":-1,"Text":"提交数据错误!","Data":null,"Success":false}`, false},
		// 全空：无法判定，按失败处理
		{"empty", `{}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var env Envelope
			if err := json.Unmarshal([]byte(tc.json), &env); err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got := env.OK(); got != tc.want {
				t.Errorf("OK() = %v, want %v (json: %s)", got, tc.want, tc.json)
			}
		})
	}
}

// TestEnvelopeMessageText 验证 Text/Message 双字段兼容。
func TestEnvelopeMessageText(t *testing.T) {
	// 标准接口用 Text
	var a Envelope
	json.Unmarshal([]byte(`{"Code":300,"Text":"失败了"}`), &a)
	if a.MessageText() != "失败了" {
		t.Errorf("Text 优先失败: %q", a.MessageText())
	}
	// VerifyCode 特例用 Message
	var b Envelope
	json.Unmarshal([]byte(`{"Code":-8,"Message":"用户不存在"}`), &b)
	if b.MessageText() != "用户不存在" {
		t.Errorf("Message 回退失败: %q", b.MessageText())
	}
}

// TestBatchResultNormalization 验证逐条结果的字段差异归一化。
func TestBatchResultNormalization(t *testing.T) {
	raw := json.RawMessage(`[
		{"toUSerName":"wxid_a","textContent":"hi","isSendSuccess":true},
		{"toUserName":"wxid_b","isSendSuccess":false,"errMsg":"发送失败"},
		{"toUSerName":"wxid_c"}
	]`)
	results := parseBatchResults(raw)
	if len(results) != 3 {
		t.Fatalf("期望 3 条结果，实际 %d", len(results))
	}
	// 两种拼写都要能取到接收方
	if results[0].To != "wxid_a" || !results[0].OK {
		t.Errorf("第 1 条解析错误: %+v", results[0])
	}
	if results[1].To != "wxid_b" || results[1].OK || results[1].ErrorMessage != "发送失败" {
		t.Errorf("第 2 条解析错误: %+v", results[1])
	}
	// 无标志无错误 → 视为成功
	if !results[2].OK {
		t.Errorf("第 3 条应视为成功: %+v", results[2])
	}
}

// TestErrMgsTypo 验证 UploadFriendCircleImage 的拼写错误字段也能识别。
func TestErrMgsTypo(t *testing.T) {
	raw := json.RawMessage(`[{"imageId":"x","errMgs":"上传失败"}]`)
	results := parseBatchResults(raw)
	if len(results) != 1 || results[0].ErrorMessage != "上传失败" {
		t.Errorf("errMgs 拼写兼容失败: %+v", results)
	}
}

// TestBigIntPrecision 验证 64 位消息 ID 不丢精度。
func TestBigIntPrecision(t *testing.T) {
	// 这个值超过 2^53，JS 的 JSON.parse 会截断；Go 用 json.Number 或 string 安全
	const bigID = "199344895681685660"
	var v map[string]json.RawMessage
	json.Unmarshal([]byte(`{"new_msg_id":199344895681685660}`), &v)
	if string(v["new_msg_id"]) != bigID {
		t.Errorf("大整数解析错误: %s", v["new_msg_id"])
	}
}

// TestClientSendsKeyInQuery 验证 license key 走 query 参数（协议端唯一鉴权方式）。
func TestClientSendsKeyInQuery(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.URL.Query().Get("key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"Code":200,"Data":[{"toUSerName":"wxid_a","isSendSuccess":true}],"Success":true}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key-123", WithoutRateLimit())
	res, err := c.Message.SendText(context.Background(), &SendTextRequest{
		To:   "wxid_a",
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	if gotPath != "/message/SendTextMessage" {
		t.Errorf("路径错误: %s", gotPath)
	}
	if gotKey != "test-key-123" {
		t.Errorf("key 未通过 query 传递: %q", gotKey)
	}
	if !res.OK {
		t.Errorf("结果应为成功: %+v", res)
	}
}

// TestClientErrorMapping 验证业务失败映射成 APIError。
func TestClientErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"Code":-2,"Text":" 该链接不存在！","Success":false}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "k", WithoutRateLimit())
	_, err := c.Message.SendText(context.Background(), &SendTextRequest{To: "x", Text: "y"})
	if err == nil {
		t.Fatal("应返回错误")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("错误类型应为 *APIError，实际 %T", err)
	}
	if apiErr.Code != CodeFailKey {
		t.Errorf("Code = %d, want %d", apiErr.Code, CodeFailKey)
	}
	if !apiErr.IsKeyInvalid() {
		t.Error("IsKeyInvalid() 应为 true")
	}
}

// TestNonJSONResponse 验证非 JSON 响应（如 WS 握手失败）不会 panic。
func TestNonJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"无效的key"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "k", WithoutRateLimit())
	_, err := c.Message.SendText(context.Background(), &SendTextRequest{To: "x", Text: "y"})
	if err == nil {
		t.Fatal("应返回错误")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("错误类型应为 *APIError，实际 %T", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}

// TestRateLimiter 验证令牌桶按预期放行。
func TestRateLimiter(t *testing.T) {
	lim := NewRateLimiter(2, 100*time.Millisecond) // 100ms 内 2 次
	ctx := context.Background()

	start := time.Now()
	// 前 2 次立即通过
	if err := lim.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := lim.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Errorf("前两次应立即可用，耗时 %v", elapsed)
	}
	// 第 3 次必须等待令牌补充
	start = time.Now()
	if err := lim.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("第三次应等待约 50ms，实际只用了 %v", elapsed)
	}
}

// TestRateLimiterContextCancel 验证 ctx 取消能中断等待。
func TestRateLimiterContextCancel(t *testing.T) {
	lim := NewRateLimiter(1, time.Hour) // 极慢补充
	lim.Wait(context.Background())      // 耗尽唯一令牌

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := lim.Wait(ctx); err == nil {
		t.Error("应该在 ctx 超时时返回错误")
	}
}

// TestLoginStateMachine 验证 5 个状态的判定。
func TestLoginStateMachine(t *testing.T) {
	cases := []struct {
		state      string
		wantOnline bool
		wantTerm   bool
	}{
		{"waiting", false, false},
		{"login_processing", false, false},
		{"ipad_required", false, false},
		{"online", true, true},
		{"security_verify_required", false, true},
	}
	for _, tc := range cases {
		st := &LoginState{LoginState: tc.state}
		if st.IsOnline() != tc.wantOnline {
			t.Errorf("%s IsOnline = %v", tc.state, st.IsOnline())
		}
		if st.IsTerminal() != tc.wantTerm {
			t.Errorf("%s IsTerminal = %v", tc.state, st.IsTerminal())
		}
	}
}

// TestKeyMasking 验证日志脱敏。
func TestKeyMasking(t *testing.T) {
	c := NewClient("http://x", "abcdefghijklmnop")
	if got := c.Key(); got != "abcd****mnop" {
		t.Errorf("脱敏错误: %s", got)
	}
}
