// Package helps: DeepSeek thinking-mode compatibility shim.
//
// DeepSeek V4 thinking-mode requires every assistant turn in the request
// messages[] history to carry a reasoning_content field (empty string allowed).
// The Anthropic→OpenAI translator only emits that field when the source
// message contained a Claude {type:"thinking"} block, so histories that mix
// non-thinking assistant turns with thinking ones get rejected by DeepSeek
// with:
//
//	400 {"error":{"message":"The `content[].thinking` in the thinking mode
//	must be passed back to the API."}}
//
// This helper detects DeepSeek-bound requests at the executor layer and pads
// missing reasoning_content with an empty string.
//
// Detection signals mirror Hermes Agent PR #15407
// (https://github.com/NousResearch/hermes-agent/pull/15407) and the
// structural pattern mirrors the upstream Kimi fix
// (https://github.com/router-for-me/CLIProxyAPI/commit/52364af5).
package helps

import (
	"net/url"
	"strconv"
	"strings"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const deepSeekHostSuffix = "deepseek.com"
const deepSeekSignal = "deepseek"

// IsDeepSeekTarget reports whether the upstream being targeted is DeepSeek.
// Any one of the following signals is sufficient:
//   - auth.Provider / auth.Attributes["compat_name"] / auth.Attributes["provider_key"]
//     contains "deepseek" (case-insensitive).
//   - auth.Attributes["base_url"] resolves to a host ending in ".deepseek.com"
//     or exactly "deepseek.com".
//   - model contains "deepseek" (case-insensitive).
//
// auth may be nil; in that case only the model name is consulted.
func IsDeepSeekTarget(auth *cliproxyauth.Auth, model string) bool {
	if containsDeepSeek(model) {
		return true
	}
	if auth == nil {
		return false
	}
	if containsDeepSeek(auth.Provider) {
		return true
	}
	if auth.Attributes != nil {
		if containsDeepSeek(auth.Attributes["compat_name"]) {
			return true
		}
		if containsDeepSeek(auth.Attributes["provider_key"]) {
			return true
		}
		if baseURL := strings.TrimSpace(auth.Attributes["base_url"]); baseURL != "" {
			if hostMatchesDeepSeek(baseURL) {
				return true
			}
		}
	}
	return false
}

// PadDeepSeekReasoningContent walks messages[] in an OpenAI Chat Completions
// request body and ensures every assistant message carries a reasoning_content
// field. Messages that already define the field (including empty string) are
// left untouched. Non-assistant roles are ignored.
//
// Returns the original body on malformed JSON or when no messages array is
// present. sjson write errors are swallowed — padding is a best-effort
// compatibility shim and must never block the request.
func PadDeepSeekReasoningContent(body []byte) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}
	out := body
	index := 0
	messages.ForEach(func(_, msg gjson.Result) bool {
		if msg.Get("role").String() == "assistant" && !msg.Get("reasoning_content").Exists() {
			path := "messages." + strconv.Itoa(index) + ".reasoning_content"
			if updated, err := sjson.SetBytes(out, path, ""); err == nil {
				out = updated
			}
		}
		index++
		return true
	})
	return out
}

func containsDeepSeek(s string) bool {
	if s == "" {
		return false
	}
	return strings.Contains(strings.ToLower(s), deepSeekSignal)
}

func hostMatchesDeepSeek(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		host = strings.ToLower(strings.TrimSpace(raw))
	}
	return host == deepSeekHostSuffix || strings.HasSuffix(host, "."+deepSeekHostSuffix)
}
