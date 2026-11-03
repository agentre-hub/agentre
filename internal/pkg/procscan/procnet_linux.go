//go:build linux

package procscan

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
)

// clkTck 是 /proc/<pid>/stat 里 starttime 的时钟频率（每秒滴答数）。
// 不用 cgo / sysconf：Linux 用户态可见的 USER_HZ 在所有主流架构上固定为 100，
// 内核为保证 ABI 稳定对用户态始终按 USER_HZ 导出。
const clkTck = 100

// statInfo 是 /proc/<pid>/stat 里我们关心的几个字段。
type statInfo struct {
	State      byte
	PPID       int
	StartTicks uint64
}

// parseStat 解析一行 /proc/<pid>/stat。comm 可含空格与括号，所以从最后一个
// 右括号之后开始切字段：state(3) ppid(4) … starttime(22)。
func parseStat(line string) (statInfo, error) {
	end := strings.LastIndexByte(line, ')')
	if end < 0 {
		return statInfo{}, errors.New("procscan: stat has no comm")
	}
	f := strings.Fields(line[end+1:])
	// f[0]=字段3(state)，字段22(starttime) 即 f[19]。
	if len(f) < 20 || len(f[0]) != 1 {
		return statInfo{}, errors.New("procscan: stat has too few fields")
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return statInfo{}, err
	}
	ticks, err := strconv.ParseUint(f[19], 10, 64)
	if err != nil {
		return statInfo{}, err
	}
	return statInfo{State: f[0][0], PPID: ppid, StartTicks: ticks}, nil
}

// startMs 把开机时间 btime（unix 秒）与 starttime 滴答数换算成 unix 毫秒。
func startMs(btime int64, ticks uint64) int64 {
	return btime*1000 + int64(ticks)*1000/clkTck
}

// parseBtime 从 /proc/stat 内容里取 btime。
func parseBtime(b []byte) (int64, error) {
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		}
	}
	return 0, errors.New("procscan: btime not found")
}

// markerFromEnviron 只取环境块里的 AGENTRE_SESSION，其余变量（可能含密钥）一律丢弃。
func markerFromEnviron(blob []byte) string {
	prefix := []byte(MarkerEnv + "=")
	for _, kv := range bytes.Split(blob, []byte{0}) {
		if bytes.HasPrefix(kv, prefix) {
			return string(kv[len(prefix):])
		}
	}
	return ""
}

// parseCmdline 把 NUL 分隔的 cmdline 切成 argv。
func parseCmdline(b []byte) []string {
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\x00")
}

// parseNetTCP 解析 /proc/net/tcp 或 tcp6，只保留 LISTEN(0A) 行，返回 socket inode → 本地端口。
func parseNetTCP(b []byte) map[uint64]uint16 {
	out := map[uint64]uint16{}
	for i, line := range strings.Split(string(b), "\n") {
		if i == 0 {
			continue // 表头
		}
		f := strings.Fields(line)
		// sl local rem st tx:rx tr:tm retrnsmt uid timeout inode
		if len(f) < 10 || f[3] != "0A" {
			continue
		}
		colon := strings.LastIndexByte(f[1], ':')
		if colon < 0 {
			continue
		}
		port, err := strconv.ParseUint(f[1][colon+1:], 16, 16)
		if err != nil {
			continue
		}
		ino, err := strconv.ParseUint(f[9], 10, 64)
		if err != nil || ino == 0 {
			continue
		}
		out[ino] = uint16(port)
	}
	return out
}

// parseSocketLink 解析 /proc/<pid>/fd/N 的链接目标 "socket:[inode]"。
func parseSocketLink(s string) (uint64, bool) {
	v, ok := strings.CutPrefix(s, "socket:[")
	if !ok {
		return 0, false
	}
	v, ok = strings.CutSuffix(v, "]")
	if !ok {
		return 0, false
	}
	ino, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return ino, true
}
