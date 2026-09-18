// Package pathdepth 提供"路径遍历是否会走到根以上"的共享判据。workspacefs 的
// 相对路径解析与 remotefs 的绝对路径规范化都用它,保证两处的 ".." 取舍一致。
package pathdepth

import "strings"

// EscapesRoot 按 "/" 切分路径并模拟遍历:任何 ".." 段把深度打到负数(即在根处
// 再往上一级)即返回 true。必须在 filepath.Clean 之前判定 —— Clean 会静默吞掉
// 根以上的 "..",检查就失效了。
func EscapesRoot(path string) bool {
	depth := 0
	for _, seg := range strings.Split(path, "/") {
		switch seg {
		case "", ".":
			// 空段(来自 / 或 //)与 . 段不改变深度
		case "..":
			if depth == 0 {
				return true
			}
			depth--
		default:
			depth++
		}
	}
	return false
}
