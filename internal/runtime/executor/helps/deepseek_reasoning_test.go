package helps

import (
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

// --- IsDeepSeekTarget ---

func TestIsDeepSeekTarget_ModelName(t *testing.T) {
	if !IsDeepSeekTarget(nil, "deepseek-v4-pro") {
		t.Error("model containing 'deepseek' should match")
	}
	if !IsDeepSeekTarget(nil, "DeepSeek-Chat") {
		t.Error("model match should be case-insensitive")
	}
	if IsDeepSeekTarget(nil, "gpt-5") {
		t.Error("unrelated model should not match")
	}
	if IsDeepSeekTarget(nil, "") {
		t.Error("empty model should not match")
	}
}

func TestIsDeepSeekTarget_ProviderName(t *testing.T) {
	auth := &cliproxyauth.Auth{Provider: "deepseek"}
	if !IsDeepSeekTarget(auth, "some-model") {
		t.Error("auth.Provider='deepseek' should match")
	}
	auth.Provider = "DEEPSEEK"
	if !IsDeepSeekTarget(auth, "some-model") {
		t.Error("auth.Provider match should be case-insensitive")
	}
	auth.Provider = "openrouter"
	if IsDeepSeekTarget(auth, "some-model") {
		t.Error("unrelated provider should not match")
	}
}

func TestIsDeepSeekTarget_CompatName(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "openai-compatibility",
		Attributes: map[string]string{
			"compat_name": "deepseek-main",
		},
	}
	if !IsDeepSeekTarget(auth, "some-model") {
		t.Error("compat_name containing 'deepseek' should match")
	}
}

func TestIsDeepSeekTarget_ProviderKey(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"provider_key": "DEEPSEEK",
		},
	}
	if !IsDeepSeekTarget(auth, "some-model") {
		t.Error("provider_key match should be case-insensitive")
	}
}

func TestIsDeepSeekTarget_BaseURL(t *testing.T) {
	tests := []struct {
		baseURL string
		want    bool
	}{
		{"https://api.deepseek.com/v1", true},
		{"https://api.deepseek.com", true},
		{"http://api.deepseek.com/chat/completions", true},
		{"https://api.deepseek.com", true},
		{"https://evil-deepseek.com.attacker.net/v1", false},
		{"https://notdeepseek.com/v1", false},
		{"", false},
	}
	for _, tt := range tests {
		auth := &cliproxyauth.Auth{
			Attributes: map[string]string{"base_url": tt.baseURL},
		}
		got := IsDeepSeekTarget(auth, "some-model")
		if got != tt.want {
			t.Errorf("baseURL=%q got=%v want=%v", tt.baseURL, got, tt.want)
		}
	}
}

func TestIsDeepSeekTarget_NilAuth(t *testing.T) {
	if !IsDeepSeekTarget(nil, "deepseek-v4-pro") {
		t.Error("nil auth + deepseek model should still match via model")
	}
	if IsDeepSeekTarget(nil, "gpt-5") {
		t.Error("nil auth + non-deepseek model should not match")
	}
}

func TestIsDeepSeekTarget_NilAttributes(t *testing.T) {
	auth := &cliproxyauth.Auth{Provider: "openrouter"}
	if IsDeepSeekTarget(auth, "some-model") {
		t.Error("nil attributes + no other signal should not match")
	}
}

// --- PadDeepSeekReasoningContent ---

func TestPad_AssistantNoField(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","content":"hi"}]}`)
	out := PadDeepSeekReasoningContent(body)
	v := gjson.GetBytes(out, "messages.0.reasoning_content")
	if !v.Exists() {
		t.Error("should pad missing reasoning_content on assistant")
	}
	if v.String() != "" {
		t.Errorf("expected empty string, got %q", v.String())
	}
}

func TestPad_AssistantExistingNonEmpty(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","content":"hi","reasoning_content":"thinking here"}]}`)
	out := PadDeepSeekReasoningContent(body)
	v := gjson.GetBytes(out, "messages.0.reasoning_content")
	if v.String() != "thinking here" {
		t.Errorf("should preserve existing reasoning_content, got %q", v.String())
	}
}

func TestPad_AssistantExistingEmpty(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","content":"hi","reasoning_content":""}]}`)
	out := PadDeepSeekReasoningContent(body)
	v := gjson.GetBytes(out, "messages.0.reasoning_content")
	if v.String() != "" {
		t.Errorf("should preserve existing empty reasoning_content, got %q", v.String())
	}
}

func TestPad_NonAssistantUntouched(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hello"},{"role":"system","content":"you are helpful"},{"role":"tool","tool_call_id":"t1","content":"result"}]}`)
	out := PadDeepSeekReasoningContent(body)
	if string(out) != string(body) {
		t.Error("non-assistant messages should not be touched")
	}
}

func TestPad_MixedMessages(t *testing.T) {
	body := []byte(`{
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi","reasoning_content":"thinking"},
			{"role":"user","content":"do stuff"},
			{"role":"assistant","content":"done"},
			{"role":"assistant","content":"also","tool_calls":[{"id":"tc1","type":"function","function":{"name":"f","arguments":"{}"}}]}
		]
	}`)
	out := PadDeepSeekReasoningContent(body)
	// messages[1] already has reasoning_content — preserved
	v1 := gjson.GetBytes(out, "messages.1.reasoning_content")
	if v1.String() != "thinking" {
		t.Errorf("messages[1] should preserve existing, got %q", v1.String())
	}
	// messages[3] has no reasoning_content — padded
	v3 := gjson.GetBytes(out, "messages.3.reasoning_content")
	if !v3.Exists() {
		t.Error("messages[3] should be padded")
	}
	// messages[4] has no reasoning_content — padded
	v4 := gjson.GetBytes(out, "messages.4.reasoning_content")
	if !v4.Exists() {
		t.Error("messages[4] should be padded")
	}
}

func TestPad_InvalidJSON(t *testing.T) {
	body := []byte(`not json`)
	out := PadDeepSeekReasoningContent(body)
	if string(out) != string(body) {
		t.Error("invalid JSON should be returned as-is")
	}
}

func TestPad_NoMessages(t *testing.T) {
	body := []byte(`{"model":"test"}`)
	out := PadDeepSeekReasoningContent(body)
	if string(out) != string(body) {
		t.Error("body without messages should be returned as-is")
	}
}

func TestPad_EmptyMessages(t *testing.T) {
	body := []byte(`{"messages":[]}`)
	out := PadDeepSeekReasoningContent(body)
	if string(out) != string(body) {
		t.Error("empty messages array should be returned as-is")
	}
}

func TestPad_EmptyBody(t *testing.T) {
	out := PadDeepSeekReasoningContent([]byte{})
	if len(out) != 0 {
		t.Error("empty body should be returned as-is")
	}
}
