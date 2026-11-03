//go:build darwin

package procscan

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
	"strconv"
	"strings"
)

var errBadProcArgs = errors.New("procscan: malformed kern.procargs2 buffer")

// parseProcArgs 解析 kern.procargs2 缓冲区：4 字节 argc、exec_path、NUL 填充、
// argc 个 argv、随后是环境。出于隐私只取出 AGENTRE_SESSION 的值，其余环境变量一概丢弃。
// 环境末尾没有 NUL 结尾的片段视为被截断，不采信。
func parseProcArgs(buf []byte) (argv []string, marker string, err error) {
	if len(buf) < 4 {
		return nil, "", errBadProcArgs
	}
	argc := int(int32(binary.LittleEndian.Uint32(buf)))
	if argc < 0 {
		return nil, "", errBadProcArgs
	}
	rest := buf[4:]
	// 跳过 exec_path 及其后的 NUL 填充。
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil, "", errBadProcArgs
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	for n := 0; n < argc; n++ {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			return nil, "", errBadProcArgs
		}
		argv = append(argv, string(rest[:j]))
		rest = rest[j+1:]
	}
	prefix := []byte(MarkerEnv + "=")
	for len(rest) > 0 {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			break
		}
		if entry := rest[:j]; bytes.HasPrefix(entry, prefix) {
			marker = string(entry[len(prefix):])
			break
		}
		rest = rest[j+1:]
	}
	return argv, marker, nil
}

// parseLsofPorts 解析 `lsof -F pcn -iTCP -sTCP:LISTEN` 的输出，返回 pid -> 去重排序后的端口。
// IPv4 / IPv6 同端口只算一个。
func parseLsofPorts(out []byte) map[int][]uint16 {
	seen := map[int]map[uint16]struct{}{}
	pid := 0
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			n, err := strconv.Atoi(line[1:])
			if err != nil {
				pid = 0
				continue
			}
			pid = n
		case 'n':
			if pid == 0 {
				continue
			}
			name := line[1:]
			k := strings.LastIndexByte(name, ':')
			if k < 0 {
				continue
			}
			port, err := strconv.ParseUint(name[k+1:], 10, 16)
			if err != nil {
				continue
			}
			if seen[pid] == nil {
				seen[pid] = map[uint16]struct{}{}
			}
			seen[pid][uint16(port)] = struct{}{}
		}
	}
	res := make(map[int][]uint16, len(seen))
	for p, set := range seen {
		ports := make([]uint16, 0, len(set))
		for port := range set {
			ports = append(ports, port)
		}
		sort.Slice(ports, func(a, b int) bool { return ports[a] < ports[b] })
		res[p] = ports
	}
	return res
}

// parseLsofCwd 解析 `lsof -F pfn -d cwd` 的输出，返回 pid -> 工作目录。
func parseLsofCwd(out []byte) map[int]string {
	res := map[int]string{}
	pid := 0
	isCwd := false
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			n, err := strconv.Atoi(line[1:])
			if err != nil {
				pid = 0
				continue
			}
			pid, isCwd = n, false
		case 'f':
			isCwd = line[1:] == "cwd"
		case 'n':
			if pid != 0 && isCwd {
				res[pid] = line[1:]
			}
		}
	}
	return res
}
