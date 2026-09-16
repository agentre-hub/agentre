package diff

import (
	"slices"
	"strings"
)

// FromClaudeCodeEdit 把 Edit 工具的 (filePath, oldString, newString) 转成 Payload。
// replaceAll 时不知道每个实际位置在原文里的行号,统一从 1 起算,前端只用行号显示。
func FromClaudeCodeEdit(filePath, oldString, newString string, replaceAll bool) Payload {
	oldLines := splitLinesNoTrailingEmpty(oldString)
	newLines := splitLinesNoTrailingEmpty(newString)

	lines, plus, minus := diffLines(oldLines, newLines)
	hunks := []Hunk{}
	if plus+minus > 0 {
		hunks = []Hunk{{
			OldStart: 1,
			OldLines: len(oldLines),
			NewStart: 1,
			NewLines: len(newLines),
			Lines:    lines,
		}}
	}

	f := File{
		Path:       filePath,
		Kind:       KindModified,
		Hunks:      hunks,
		Plus:       plus,
		Minus:      minus,
		ReplaceAll: replaceAll,
	}
	if len(lines) > MaxLinesPerFile {
		f.Truncated = true
		f.Hunks[0].Lines = lines[:MaxLinesPerFile]
	}
	return Payload{Files: []File{f}}
}

// splitLinesNoTrailingEmpty 按 \n 切分,丢掉因末尾换行产生的空尾项。
// 兼容 \r\n: 切完后逐行 trim \r。
func splitLinesNoTrailingEmpty(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i := range parts {
		parts[i] = strings.TrimSuffix(parts[i], "\r")
	}
	return parts
}

// diffLines 用经典 LCS 表算行级 diff,返回 (lines, plusCount, minusCount)。
// 行数超过 MaxLinesPerFile 时截断: lines 仅保留前 MaxLinesPerFile 条,后续靠
// File.Truncated 在调用方设置。
func diffLines(oldLines, newLines []string) ([]Line, int, int) {
	m, n := len(oldLines), len(newLines)
	if m == 0 && n == 0 {
		return nil, 0, 0
	}

	// LCS DP table
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else if lcs[i-1][j] >= lcs[i][j-1] {
				lcs[i][j] = lcs[i-1][j]
			} else {
				lcs[i][j] = lcs[i][j-1]
			}
		}
	}

	// 回溯生成行列表
	var rev []Line
	i, j := m, n
	plus, minus := 0, 0
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && oldLines[i-1] == newLines[j-1]:
			rev = append(rev, Line{Op: OpContext, Old: i, New: j, Text: oldLines[i-1]})
			i--
			j--
		case j > 0 && (i == 0 || lcs[i][j-1] >= lcs[i-1][j]):
			rev = append(rev, Line{Op: OpAdd, New: j, Text: newLines[j-1]})
			plus++
			j--
		default:
			rev = append(rev, Line{Op: OpDel, Old: i, Text: oldLines[i-1]})
			minus++
			i--
		}
	}

	// 反转
	slices.Reverse(rev)
	return rev, plus, minus
}
