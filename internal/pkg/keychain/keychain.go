// Package keychain 抽象一层 OS 级别的密钥存储。
//
// 桌面端用它来持久化 Hub refresh_token：生产构建挂 NewSystem()；
// 没有 OS keychain 的环境（CI / 部分 Linux 容器）显式 opt-in NewFile()；
// 单测一律用 NewMemory()。
package keychain

import (
	"errors"
	"strings"
)

// ErrNotFound 表示账号下没有对应密钥。
var ErrNotFound = errors.New("keychain: secret not found")

// Keychain 抽象一层 OS 级别的密钥存储。
type Keychain interface {
	Get(account string) (string, error)
	Set(account, secret string) error
	Delete(account string) error
}

var defaultKC Keychain

// Default 返回当前注册的实现。
func Default() Keychain { return defaultKC }

// SetDefault 由 bootstrap 注入一个实现。
func SetDefault(k Keychain) { defaultKC = k }

// LLMProviderAPIKeyAccount 是桌面端 LLM 供应商 API Key 的钥匙串槽位名。键是 provider_key
// （账号级同步 id，各设备一致）；空 provider_key 没有槽位，返回 ""，调用方不得读写。
func LLMProviderAPIKeyAccount(providerKey string) string {
	providerKey = strings.TrimSpace(providerKey)
	if providerKey == "" {
		return ""
	}
	return "agentre.llm-provider." + providerKey + ".api_key"
}
