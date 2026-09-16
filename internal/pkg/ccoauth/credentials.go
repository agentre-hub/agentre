package ccoauth

import (
	"encoding/json"
	"errors"
	"os"
)

// ErrNoCredentials 表示 Keychain 或 .credentials.json 都没找到可用 OAuth token。
// 对应 OMC 的 "no_credentials" 状态（API key 用户 / 未登录用户的预期结果）。
var ErrNoCredentials = errors.New("ccoauth: no OAuth credentials available")

// Credentials 是从 Keychain / 文件 fallback 读到的 OAuth 凭证。
type Credentials struct {
	AccessToken string
}

// fileShape 兼容 OMC 看到的两种结构：嵌套 {claudeAiOauth:{…}} 和扁平 {accessToken:…}
type fileShape struct {
	ClaudeAiOauth *struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth,omitempty"`

	AccessToken string `json:"accessToken,omitempty"`
}

// ReadFileCredentials 从 .credentials.json 文件读 OAuth 凭证。
// 文件不存在 / 文件存在但没 accessToken → ErrNoCredentials。
// JSON 损坏 / 文件读失败 → 原生 error。
func ReadFileCredentials(path string) (*Credentials, error) {
	//nolint:gosec // G304: path is a CLAUDE_CONFIG_DIR-derived credentials.json, supplied by trusted caller (cc_usage_svc / agentred)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoCredentials
		}
		return nil, err
	}
	return parseCredentialsBlob(b)
}

// parseCredentialsBlob 解析 .credentials.json 或 keychain 里的同形 JSON,
// 兼容 OMC 已知的两种 shape:嵌套 {claudeAiOauth:{…}} 和扁平 {accessToken:…}。
func parseCredentialsBlob(b []byte) (*Credentials, error) {
	var shape fileShape
	if err := json.Unmarshal(b, &shape); err != nil {
		return nil, err
	}
	if shape.ClaudeAiOauth != nil && shape.ClaudeAiOauth.AccessToken != "" {
		return &Credentials{AccessToken: shape.ClaudeAiOauth.AccessToken}, nil
	}
	if shape.AccessToken != "" {
		return &Credentials{AccessToken: shape.AccessToken}, nil
	}
	return nil, ErrNoCredentials
}
