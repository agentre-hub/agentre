// Package ctlendpoint defines the on-disk handshake file that the running
// desktop writes and the `agrctl ctl` CLI reads to locate + authenticate
// against the desktop's local control endpoint (exposed on the httpgateway
// under /ctl/). The file lives in AppDataDir and is written 0600 so only the
// same OS user can read the token — that file permission is the trust boundary
// for the loopback control API.
package ctlendpoint

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// FileName 固定文件名，落在 AppDataDir 下。
const FileName = "ctl-endpoint.json"

// Endpoint 控制端点握手信息：gateway 实际 base URL + 控制 token。
type Endpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// FilePath 返回 dataDir 下的握手文件绝对路径。
func FilePath(dataDir string) string { return filepath.Join(dataDir, FileName) }

// Write 以 0600 覆盖写入握手文件。URL 为空视为错误——gateway 未运行（BaseURL 为空）时
// 不应写文件，避免 CLI 读到一个连不上的旧地址。
func Write(dataDir string, ep Endpoint) error {
	if strings.TrimSpace(ep.URL) == "" {
		return errors.New("ctlendpoint: empty url")
	}
	b, err := json.Marshal(ep)
	if err != nil {
		return err
	}
	return os.WriteFile(FilePath(dataDir), b, 0o600)
}

// Read 读取并解析握手文件。文件不存在时返回的 error 满足 errors.Is(err, os.ErrNotExist)，
// 调用方据此提示「桌面未运行 / gateway 未就绪」。
func Read(dataDir string) (Endpoint, error) {
	b, err := os.ReadFile(FilePath(dataDir))
	if err != nil {
		return Endpoint{}, err
	}
	var ep Endpoint
	if err := json.Unmarshal(b, &ep); err != nil {
		return Endpoint{}, err
	}
	return ep, nil
}
