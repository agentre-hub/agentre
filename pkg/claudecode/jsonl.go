package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrSessionJSONLNotFound 在指定 root 下没有找到 <sid>.jsonl 文件。
// 调用方应当把它视为"anchor 暂时不可用"而非致命错误（CLI 刚启动还没落盘是常见情况）。
var ErrSessionJSONLNotFound = errors.New("claudecode: session JSONL file not found")

// SessionMessage 是 claude CLI 持久化在 ~/.claude/projects/<slug>/<sid>.jsonl
// 的单条消息记录。仅暴露 chat_svc 需要的字段，其余字段（timestamp / 完整 message
// 体等）暂时不解（用 json.RawMessage 兜底足够，当前调用方不需要）。
type SessionMessage struct {
	UUID       string
	ParentUUID string
	Role       string // "user" / "assistant" / "system"

	// Text 是该消息 message.content[*].type == "text" 的第一段文本。
	// 用于"按发送的 user prompt 反查 JSONL 里对应的消息"。tool_result 类 user
	// 消息内容不是 text 块，这里会留空 —— 正好用来排除合成 user msg。
	Text string
}

// ReadSessionJSONL 在 root 下扫描所有子目录，找到名为 `<sessionID>.jsonl` 的文件
// 并按文件中出现顺序解析每行。
//
// root 通常是 `~/.claude/projects`，但允许调用方覆盖（测试或自定义 install）。
// 找不到文件时返回 ErrSessionJSONLNotFound。
func ReadSessionJSONL(root, sessionID string) ([]SessionMessage, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("claudecode: empty session id")
	}

	path, err := findSessionJSONL(root, sessionID)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path) // #nosec G304 -- path 来自 root + sid 拼接，root 由 caller 控制
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []SessionMessage
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64<<10)
	scanner.Buffer(buf, maxFrameBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		msg, ok := parseSessionMessageLine(line)
		if !ok {
			continue // 跳过坏行；JSONL 里偶发部分写入，不致命
		}
		out = append(out, msg)
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// findSessionJSONL 在 root 下递归找 <sid>.jsonl 第一处匹配。
// claude CLI 把项目目录 slug 化（cwd 转 dash），slug 不稳定，所以 glob 多 slug。
func findSessionJSONL(root, sessionID string) (string, error) {
	target := sessionID + ".jsonl"
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			// 单个子目录读不动不致命：跳过它继续扫；只在 root 本身缺失时才往上抛。
			if path == root {
				return werr
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), target) {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrSessionJSONLNotFound
		}
		return "", err
	}
	if found == "" {
		return "", ErrSessionJSONLNotFound
	}
	return found, nil
}

// FindUserAnchorByText 给定 user 发送的 prompt 文本，反向定位它对应的 JSONL 消息，
// 然后从它往回走找到**最近一条 assistant 消息**，返回 assistant 的 UUID。
//
// 为什么不返回 userMsg.ParentUUID：CLI 在每条 user msg 之前都会写若干 attachment
// 条目（hook_success / skill_listing / mcp_instructions_delta / hook_additional_context
// 等），user 的 ParentUUID 实际是 attachment 的 uuid。把 attachment uuid 喂给
// `--resume-session-at` 会让 CLI 的 interrupted-state 检测把"最后一条是 attachment"
// 判为 interrupted_turn，自动注入 "Continue from where you left off." 这条 meta msg
// 污染对话历史。锚到上一条 assistant 上面，CLI 看 prefix 末尾是 assistant，返回
// {kind:"none"}，不会注入。
//
// tool_result 类合成 user 消息（content 是 tool_result block，Text 留空）不会被
// "按文本匹配"命中，反查阶段就被排除；assistant 多 block（thinking + text + tool_use）
// 取最靠近 user 的那一条。
//
// 首轮场景：user 前面没有任何 assistant → 返回空。上层应当把 sess.ProviderSessionID
// 置空、当作全新会话发起（而不是 fork）。
func FindUserAnchorByText(msgs []SessionMessage, userText string) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" || msgs[i].Text != userText {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if msgs[j].Role == "assistant" {
				return msgs[j].UUID
			}
		}
		return ""
	}
	return ""
}

// parseSessionMessageLine 解一行 JSONL。ok=false 表示坏行(部分写入),调用方跳过即可。
func parseSessionMessageLine(line []byte) (SessionMessage, bool) {
	var raw struct {
		Type       string `json:"type"`
		UUID       string `json:"uuid"`
		ParentUUID string `json:"parentUuid"`
		Message    struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return SessionMessage{}, false
	}
	role := raw.Message.Role
	if role == "" {
		role = raw.Type
	}
	var text string
	for _, c := range raw.Message.Content {
		if c.Type == "text" && c.Text != "" {
			text = c.Text
			break
		}
	}
	return SessionMessage{
		UUID:       raw.UUID,
		ParentUUID: raw.ParentUUID,
		Role:       role,
		Text:       text,
	}, true
}

// FindUserAnchorInSessionJSONL 是 ReadSessionJSONL + FindUserAnchorByText 的等价
// 替代,但**从文件尾往回读、拿到答案就停**。
//
// 为什么要它:anchor 永远在文件末尾附近(就是刚发出去的那轮),而整文件路径要把整个
// 会话转录解析成 []SessionMessage 才开始往回找。实测 ~/.claude/projects 下一个
// 73.5MB 的 JSONL 要 483ms(warm)~984ms(cold),而这段是在 runtime 收尾里、
// **close(out) 之前**跑的 —— chat_svc 的 `for ev := range events` 因此每轮都要多等
// 这么久才能收尾。转录随对话增长,单会话累计代价是 O(轮数²)。
//
// 语义与旧路径逐字相同:取**最后一条**文本匹配的 user,再往回找最近的 assistant;
// 找不到匹配的 user、或它前面没有 assistant,都返回空串(nil error)。
func FindUserAnchorInSessionJSONL(root, sessionID, userText string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("claudecode: empty session id")
	}
	path, err := findSessionJSONL(root, sessionID)
	if err != nil {
		return "", err
	}

	var (
		seenUser bool
		anchor   string
	)
	if err := scanLinesBackward(path, func(line []byte) bool {
		msg, ok := parseSessionMessageLine(line)
		if !ok {
			return false
		}
		if !seenUser {
			// 反向遇到的第一条文本匹配 user,就是正向意义上的「最后一条」。
			if msg.Role == "user" && msg.Text == userText {
				seenUser = true
			}
			return false
		}
		if msg.Role == "assistant" {
			anchor = msg.UUID
			return true
		}
		return false
	}); err != nil {
		return "", err
	}
	return anchor, nil
}

// backwardChunkBytes 是反向扫描每次回读的块大小。命中通常发生在第一块内。
const backwardChunkBytes = 256 << 10

// scanLinesBackward 从文件末尾向开头逐行回调 fn,fn 返回 true 即停止。
// 行内容不含结尾换行;空行跳过。跨块的半行由 carry 拼回,因此单行长度不受块大小限制
// (也正因如此,它不像 bufio.Scanner 那样有 maxFrameBytes 上限)。
func scanLinesBackward(path string, fn func(line []byte) bool) error {
	f, err := os.Open(path) // #nosec G304 -- path 来自 findSessionJSONL(root + sid 拼接),root 由 caller 控制
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return err
	}

	pos := st.Size()
	var carry []byte // 属于「起点在更早块里」的那半行
	for pos > 0 {
		n := int64(backwardChunkBytes)
		if n > pos {
			n = pos
		}
		pos -= n
		buf := make([]byte, n, n+int64(len(carry)))
		if _, err := f.ReadAt(buf, pos); err != nil {
			return err
		}
		buf = append(buf, carry...)
		for {
			i := bytes.LastIndexByte(buf, '\n')
			if i < 0 {
				break
			}
			if line := buf[i+1:]; len(line) > 0 && fn(line) {
				return nil
			}
			buf = buf[:i]
		}
		carry = buf
	}
	if len(carry) > 0 {
		fn(carry)
	}
	return nil
}
