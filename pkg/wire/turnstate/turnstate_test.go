package turnstate_test

import (
	"testing"

	"github.com/agentre-hub/agentre/pkg/wire/turnstate"
)

func TestIsFailure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  string
		code int
		want bool
	}{
		{"没有停止文案 = 正常收场", "", 0, false},
		{"用户自己按的停止不算故障", "aborted", turnstate.AbortedCode, false},
		{"有文案、不是中断 = 故障", "boom", 0, true},
		// 错误码 0 是「没有 sentinel」,不是「没出错」:CLI 直接 exit 1 正是这一档。
		{"没有 sentinel 的启动失败照样是故障", "exit status 1", 0, true},
		{"别的 sentinel 也是故障", "peer gone", -32015, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := turnstate.IsFailure(c.msg, c.code); got != c.want {
				t.Fatalf("IsFailure(%q, %d) = %v, want %v", c.msg, c.code, got, c.want)
			}
		})
	}
}
