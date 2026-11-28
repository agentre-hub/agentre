package devicefacts

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/clienv"
	"github.com/agentre-hub/agentre/internal/pkg/cliprober"
	"github.com/agentre-hub/agentre/internal/pkg/procattr"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// KnownRuntimes 是设备要逐一报告的已知运行时,顺序即上报顺序。
var KnownRuntimes = []string{"claudecode", "codex", "piagent", "antigravity"}

// minimumVersions 是有版本下限的运行时。Antigravity 的 1.5 与 cliprober 探测时的
// 门槛是同一个数(probe_antigravity.go 的 antigravityMinMajor/Minor)。
var minimumVersions = map[string]string{"antigravity": "1.5"}

// nativeLoginRuntimes 是有「CLI 原生登录」概念、要报登录态的运行时。
var nativeLoginRuntimes = map[string]bool{"claudecode": true, "codex": true}

// versionProbeTimeout 是一次 `--version` 的上限:卡住的 CLI 不该拖住整份上报。
const versionProbeTimeout = 10 * time.Second

// RuntimeProbe 探已知运行时。三个函数都是接缝:生产走 DefaultRuntimeProbe。
type RuntimeProbe struct {
	// Resolve 找这台设备上某种运行时的可执行文件。
	Resolve func(runtime string) (path string, found bool)
	// Version 读可执行文件自报的版本(原文,Probe 负责从里面取版本号)。
	Version func(ctx context.Context, path string) (string, error)
	// Login 判 Claude Code / Codex 的原生登录态(NativeLogin* 之一)。
	Login func(runtime string) string
}

// DefaultRuntimeProbe 按 PATH 找 CLI(cliOverride 给出的本机覆盖路径优先)、跑
// `--version`、看 CLI 自己的凭据在不在。cliOverride 为 nil 表示没有覆盖。
func DefaultRuntimeProbe(cliOverride func(runtime string) string) RuntimeProbe {
	login := DefaultLoginProbe()
	return RuntimeProbe{
		Resolve: func(runtime string) (string, bool) {
			if cliOverride != nil {
				if path := strings.TrimSpace(cliOverride(runtime)); path != "" {
					return resolveExecutable(path)
				}
			}
			path, found, err := cliprober.ResolveCLIPath(runtime)
			return path, err == nil && found
		},
		Version: ReadVersion,
		Login: func(runtime string) string {
			switch runtime {
			case "claudecode":
				return login.ClaudeCode()
			case "codex":
				return login.Codex()
			}
			return ""
		},
	}
}

// Probe 每种已知运行时报一项,没装的也在。
func (p RuntimeProbe) Probe(ctx context.Context) []syncwire.RuntimeFact {
	out := make([]syncwire.RuntimeFact, 0, len(KnownRuntimes))
	for _, runtime := range KnownRuntimes {
		fact := syncwire.RuntimeFact{Runtime: runtime, MinimumVersion: minimumVersions[runtime]}
		if path, found := p.Resolve(runtime); found {
			fact.Found = true
			fact.Path = path
			if raw, err := p.Version(ctx, path); err == nil {
				fact.Version = ParseVersion(raw)
			}
			fact.BelowMinimum = BelowMinimum(fact.Version, fact.MinimumVersion)
		}
		if nativeLoginRuntimes[runtime] && p.Login != nil {
			fact.NativeLogin = p.Login(runtime)
		}
		out = append(out, fact)
	}
	return out
}

// ReadVersion 跑一次 `<path> --version`,交回标准输出。
func ReadVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = clienv.BuildEnv(nil, path)
	procattr.ApplyNoConsoleWindow(cmd)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

var versionPattern = regexp.MustCompile(`\d+(?:\.\d+)+`)

// ParseVersion 从 `--version` 的输出里取第一个「数字.数字…」;取不到为空。
func ParseVersion(raw string) string {
	return versionPattern.FindString(raw)
}

// BelowMinimum 按数字段比较:version 已知、下限已知且 version 更低时为真。缺的段按 0。
func BelowMinimum(version, minimum string) bool {
	if version == "" || minimum == "" {
		return false
	}
	have, want := versionParts(version), versionParts(minimum)
	for i := 0; i < len(have) || i < len(want); i++ {
		h, w := partAt(have, i), partAt(want, i)
		if h != w {
			return h < w
		}
	}
	return false
}

func versionParts(v string) []int {
	fields := strings.Split(v, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

func partAt(parts []int, i int) int {
	if i < len(parts) {
		return parts[i]
	}
	return 0
}
