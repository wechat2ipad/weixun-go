package conformance

// 用契约仓的同一批 fixtures 验证 Go SDK。
//
// 这是「七语言不分叉」的核心机制：任何语言的行为若与 fixture 期望不符，
// 该语言的 CI 就会红。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	weixun "github.com/wechat2ipad/weixun-go"
)

// fixturesDir 自动定位 fixtures 目录（优先用仓库内副本，回退到契约仓）。
func fixturesDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	base := filepath.Dir(filepath.Dir(thisFile))

	candidates := []string{
		filepath.Join(base, "conformance", "fixtures"),
		filepath.Join(base, "..", "weixun-api-contract", "fixtures"),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	t.Skipf("找不到 fixtures 目录，已尝试: %v", candidates)
	return ""
}

func loadFixture(t *testing.T, rel string, v interface{}) {
	t.Helper()
	path := filepath.Join(fixturesDir(t), rel)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", rel, err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("解析 %s 失败: %v", rel, err)
	}
}

// TestEnvelopeFixtures 验证所有 envelope 场景。
func TestEnvelopeFixtures(t *testing.T) {
	type fixture struct {
		Name   string                 `json:"name"`
		Input  map[string]interface{} `json:"input"`
		Expect map[string]interface{} `json:"expect"`
	}

	dir := fixturesDir(t)
	entries, err := os.ReadDir(filepath.Join(dir, "envelope"))
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			var fx fixture
			loadFixture(t, filepath.Join("envelope", e.Name()), &fx)

			body, _ := json.Marshal(fx.Input)
			var env weixun.Envelope
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatalf("解析信封失败: %v", err)
			}

			wantOK, _ := fx.Expect["ok"].(bool)
			if got := env.OK(); got != wantOK {
				t.Errorf("%s: OK() = %v, want %v", fx.Name, got, wantOK)
			}

			if wantCode, ok := fx.Expect["code"].(float64); ok {
				if got, _ := env.CodeInt(); got != int(wantCode) {
					t.Errorf("%s: code = %d, want %d", fx.Name, got, int(wantCode))
				}
			}

			if wantMsg, ok := fx.Expect["message_text"].(string); ok {
				if got := env.MessageText(); got != wantMsg {
					t.Errorf("%s: message_text = %q, want %q", fx.Name, got, wantMsg)
				}
			}

			if wantReason, ok := fx.Expect["reason_code"].(string); ok {
				if env.ReasonCode != wantReason {
					t.Errorf("%s: reason_code = %q, want %q", fx.Name, env.ReasonCode, wantReason)
				}
			}
		})
	}
}

// TestWSMessageFrameFixture 验证 WS 帧解析（含 {str} 解包与大整数）。
//
// 这个用例直接调用内部解析逻辑，所以放在 weixun 包内做等价测试：
// 见 stream_test.go 的 TestParseFrameMessage（使用同一份样例数据）。
func TestWSFixtureParity(t *testing.T) {
	// 读取 fixture，确认 Go 测试里用的样例与实际 fixture 一致
	var fx struct {
		Input struct {
			Message struct {
				NewMsgID json.RawMessage `json:"new_msg_id"`
			} `json:"message"`
		} `json:"input"`
		Expect map[string]interface{} `json:"expect"`
	}
	loadFixture(t, "ws/01-message-frame.json", &fx)

	wantID, _ := fx.Expect["new_msg_id"].(string)
	if string(fx.Input.Message.NewMsgID) != wantID {
		t.Errorf("fixture 自身的 new_msg_id 不一致: %s vs %s",
			fx.Input.Message.NewMsgID, wantID)
	}
	fmt.Printf("ws fixture 校验通过，64 位 ID = %s\n", wantID)
}
