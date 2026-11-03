package procscan

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// maxCommandLen 是展示用命令行的字节上限，超出截断。
const maxCommandLen = 512

// Claim 从快照里挑出 convID 会话值得展示的进程。
//
// 候选是标记等于 convID 的进程，排除宿主登记的 CLI（cliPIDs）。候选满足任一条即列出：
// 监听了 TCP 端口；父进程不在快照里；父进程标记不是 convID（已脱离的根）。
// 过继给宿主（如 PID 1）的孤儿，其父进程没有标记，同样算已脱离。
// 轮内前台命令（父为 CLI）与已脱离进程的子进程不列，除非它自己监听端口。
func Claim(snap []Proc, convID string, cliPIDs map[int]struct{}) []Listed {
	if convID == "" {
		return nil
	}
	byPID := make(map[int]Proc, len(snap))
	for _, p := range snap {
		byPID[p.PID] = p
	}
	var picked []Proc
	for _, p := range snap {
		if p.Marker != convID {
			continue
		}
		if _, isCLI := cliPIDs[p.PID]; isCLI {
			continue
		}
		parent, ok := byPID[p.PPID]
		if len(p.Ports) > 0 || !ok || parent.Marker != convID {
			picked = append(picked, p)
		}
	}
	sort.Slice(picked, func(i, j int) bool {
		if picked[i].StartMs != picked[j].StartMs {
			return picked[i].StartMs < picked[j].StartMs
		}
		return picked[i].PID < picked[j].PID
	})
	out := make([]Listed, 0, len(picked))
	for _, p := range picked {
		out = append(out, Listed{
			PID:         p.PID,
			Command:     joinCommand(p.Argv),
			Ports:       p.Ports,
			StartedAtMs: p.StartMs,
			Cwd:         p.Cwd,
		})
	}
	return out
}

// joinCommand 拼出展示用命令行：始终是合法 UTF-8（远程会话经 proto3 string 回传，
// 非法字节会让整个列表编码失败），截断只落在字符边界上。
func joinCommand(argv []string) string {
	cmd := strings.ToValidUTF8(strings.Join(argv, " "), "\uFFFD")
	if len(cmd) <= maxCommandLen {
		return cmd
	}
	cut := maxCommandLen
	for cut > 0 && !utf8.RuneStart(cmd[cut]) {
		cut--
	}
	return cmd[:cut]
}
