package repoquery

import "strings"

// likeEscaper 把用户输入里的 LIKE 元字符转成字面量（配合 ESCAPE '\'）：搜一个
// `_` 不该把整个库都搜出来，「100%」也不该退化成「1、0、0 加任意后缀」。
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EscapeLike 转义 LIKE 模式里的元字符，调用方再自行拼 `%…%` 与 `ESCAPE '\'`。
func EscapeLike(s string) string { return likeEscaper.Replace(s) }
