// Package mcpbridge 把注入给 piagent 的 HTTP MCP server 转成一个 pi 扩展可读的
// 配置 + 一份内嵌的桥接扩展（bridge.mjs）。pi 无原生 MCP，只能用 JS 扩展加工具。
package mcpbridge

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

// ConfigEnvVar 是 bridge.mjs 读取配置文件路径的环境变量名。
const ConfigEnvVar = "AGENTRE_PI_MCP_CONFIG"

//go:embed bridge.mjs
var bridgeSource []byte

type bridgeServer struct {
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Tools   []string          `json:"tools,omitempty"`
}

type bridgeConfig struct {
	Servers []bridgeServer `json:"servers"`
}

func extDir() (string, error) {
	root, err := paths.AppDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "piagent", "ext"), nil
}

// Materialize 把内嵌的 bridge.mjs 写到 <AppDataDir>/piagent/ext/，文件名带内容哈希
// （版本隔离 + 幂等：同哈希已存在则不重写），返回绝对路径。
func Materialize() (string, error) {
	dir, err := extDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256(bridgeSource)
	// 取 sha256 前 16 hex 作内容指纹(仅用于版本隔离/缓存失效, 非安全用途, 64bit 足够)。
	name := fmt.Sprintf("agentre-mcp-bridge-%s.mjs", hex.EncodeToString(sum[:])[:16])
	path := filepath.Join(dir, name)
	if _, statErr := os.Stat(path); statErr == nil {
		return path, nil
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	if err := os.WriteFile(path, bridgeSource, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// RenderConfig 把注入的 MCPServerSpec 列表渲染成 bridge.mjs 读的 JSON，写到会话私有
// 路径 <AppDataDir>/piagent/ext/cfg/<sessionID>.json，返回绝对路径。绝不写用户全局
// MCP 配置目录。
func RenderConfig(specs []agentruntime.MCPServerSpec, sessionID int64) (string, error) {
	path, err := configPath(sessionID)
	if err != nil {
		return "", err
	}
	// cfg 目录只存含鉴权 token 的会话私有配置，限制为 0o700（仅本用户）。
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	cfg := bridgeConfig{Servers: make([]bridgeServer, 0, len(specs))}
	for _, s := range specs {
		cfg.Servers = append(cfg.Servers, bridgeServer{Name: s.Name, URL: s.URL, Headers: s.Headers, Tools: s.Tools})
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	// config 内含 Authorization: Bearer <token>，必须 0o600，不能世界可读。
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// RemoveConfig 删除某会话的渲染配置（含 token）。turn 结束、pi 子进程退出后调用——
// bridge.mjs 仅在 pi 启动时读一次配置，之后不再读该文件，故 turn 末删除安全，避免含
// 凭证的配置文件在 AppDataDir 里随会话数无限累积。文件不存在视为成功（幂等）。
func RemoveConfig(sessionID int64) error {
	path, err := configPath(sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// configPath 返回某会话渲染配置的确定路径 <AppDataDir>/piagent/ext/cfg/<sessionID>.json。
func configPath(sessionID int64) (string, error) {
	dir, err := extDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cfg", fmt.Sprintf("%d.json", sessionID)), nil
}
