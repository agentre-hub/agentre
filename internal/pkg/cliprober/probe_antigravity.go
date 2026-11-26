package cliprober

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/clienv"
)

// antigravityBinary 是 Antigravity CLI 安装后的命令名（下载包内的可执行文件叫 antigravity，
// 安装后的命令是 agy）。
const antigravityBinary = "agy"

// resolveAntigravityBinary 是查找 agy 的注入点：clienv 的增强 PATH 还会并入登录 shell
// 的 PATH 与用户工具目录（~/.local/bin 等），装了 agy 的机器上「PATH 里没有」的用例
// 照样找得到。生产恒为 clienv.ResolveBinaryForEnv，只有单测替换。
var resolveAntigravityBinary = clienv.ResolveBinaryForEnv

// antigravityMinMajor / antigravityMinMinor 是 agy 的最低版本门槛（1.5）。
const (
	antigravityMinMajor = 1
	antigravityMinMinor = 5
)

// probeAntigravity 探测 agy：解析可执行文件（CLIPath 覆盖优先，否则按 agy 在 PATH 上找），
// 跑 `agy --version`，校验 ≥1.5，成功时回版本串。--version 不需要登录与网络，
// 所以不跑 ping 一轮，也不依赖供应商 / 网关。查找与子进程环境走 clienv 的增强 PATH，
// 与运行时 cliprocess.Start 同一口径 —— 探测找得到的，会话才一定起得来，反之亦然。
func probeAntigravity(ctx context.Context, req ProbeRequest) (*ProbeResponse, error) {
	requested := strings.TrimSpace(req.CLIPath)
	if requested == "" {
		requested = antigravityBinary
	}
	binary, ok := resolveAntigravityBinary(requested, clienv.BuildEnv(req.Env, requested))
	if !ok {
		return nil, fmt.Errorf("未安装 agy（Antigravity CLI）: %s", requested)
	}

	//nolint:gosec // G204: binary 来自 agent backend 配置的 CLIPath(或默认 agy),经 clienv 解析,非请求输入
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.Env = clienv.BuildEnv(req.Env, binary)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("未安装 agy（Antigravity CLI）: %s", binary)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, errors.New(formatExitDetail("agy 进程", ee.ExitCode(), stderr.String()))
		}
		return nil, err
	}

	version := strings.TrimSpace(stdout.String())
	major, minor, ok := parseMajorMinor(version)
	if !ok {
		return nil, fmt.Errorf("无法识别 agy 版本: %q", truncateStderr(version))
	}
	if major < antigravityMinMajor || (major == antigravityMinMajor && minor < antigravityMinMinor) {
		return nil, fmt.Errorf("agy %s 版本过低，需要 %d.%d 或更新", version, antigravityMinMajor, antigravityMinMinor)
	}
	return &ProbeResponse{Text: version}, nil
}

// parseMajorMinor 解析 "1.5.16" / "1.5" 形式的版本串前两段。
func parseMajorMinor(v string) (major, minor int, ok bool) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return major, minor, true
}
