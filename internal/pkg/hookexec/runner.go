// Package hookexec 按声明的解释器执行脚本 Hook：写临时文件后起子进程，
// 注入 env、限时限输出。新增解释器 = 往 registry 加一条（OCP）。
package hookexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

var (
	errUnknownInterpreter      = errors.New("hookexec: unknown interpreter")
	errInterpreterNotInstalled = errors.New("hookexec: interpreter not installed")
)

// ScriptRunner 执行一段脚本并返回采集到的输出。
type ScriptRunner interface {
	Run(ctx context.Context, spec RunSpec) (*RunResult, error)
}

// RunSpec 描述一次脚本执行所需的全部输入。
type RunSpec struct {
	Interpreter     string
	InterpreterPath string
	Command         string
	Env             map[string]string
	Timeout         time.Duration
	MaxOutputBytes  int
}

// RunResult 是一次脚本执行的采集结果。
type RunResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Duration time.Duration
	TimedOut bool
}

// interp 描述一个解释器如何被调用。
type interp struct {
	Bin  string   // 已 LookPath 解析的绝对/可执行名
	Args []string // 脚本文件路径之前的固定参数
	Ext  string   // 临时脚本文件扩展名
}

type interpDef struct {
	candidates []string // 按序探测的二进制名
	args       []string
	ext        string
	goos       []string // 空=全平台;仅 cmd/powershell 标 {"windows"}
}

var registry = map[string]interpDef{
	"bash":       {candidates: []string{"bash"}, ext: ".sh"},
	"sh":         {candidates: []string{"sh"}, ext: ".sh"},
	"node":       {candidates: []string{"node"}, ext: ".mjs"},
	"python":     {candidates: []string{"python3", "python"}, ext: ".py"},
	"pwsh":       {candidates: []string{"pwsh"}, args: []string{"-NoProfile", "-File"}, ext: ".ps1"},
	"powershell": {candidates: []string{"powershell"}, args: []string{"-NoProfile", "-File"}, ext: ".ps1", goos: []string{"windows"}},
	"cmd":        {candidates: []string{"cmd"}, args: []string{"/c"}, ext: ".bat", goos: []string{"windows"}},
}

// interpOrder 决定 Probe 输出顺序(map 无序)。
var interpOrder = []string{"bash", "sh", "node", "python", "pwsh", "powershell", "cmd"}

// Available 是一个解释器在当前机器上的可用性。
type Available struct {
	Key       string `json:"key"`
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
}

func appliesTo(def interpDef, goos string) bool {
	if len(def.goos) == 0 {
		return true
	}
	return slices.Contains(def.goos, goos)
}

// Probe 列出在 goos 平台下适用的解释器及其安装情况。
func Probe(goos string) []Available {
	out := make([]Available, 0, len(interpOrder))
	for _, key := range interpOrder {
		def := registry[key]
		if !appliesTo(def, goos) {
			continue
		}
		a := Available{Key: key}
		for _, name := range def.candidates {
			if bin, err := exec.LookPath(name); err == nil {
				a.Path, a.Installed = bin, true
				break
			}
		}
		out = append(out, a)
	}
	return out
}

// Resolve 校验解释器并解析其二进制路径;interpreterPath 非空时覆盖二进制(args/ext 仍取预设)。
func resolve(interpreter, interpreterPath string) (*interp, error) {
	def, ok := registry[interpreter]
	if !ok {
		return nil, fmt.Errorf("%w: %q", errUnknownInterpreter, interpreter)
	}
	if p := strings.TrimSpace(interpreterPath); p != "" {
		if info, err := os.Stat(p); err != nil || info.IsDir() {
			return nil, fmt.Errorf("%w: %q", errInterpreterNotInstalled, p)
		}
		return &interp{Bin: p, Args: def.args, Ext: def.ext}, nil
	}
	for _, name := range def.candidates {
		if bin, err := exec.LookPath(name); err == nil {
			return &interp{Bin: bin, Args: def.args, Ext: def.ext}, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", errInterpreterNotInstalled, interpreter)
}
