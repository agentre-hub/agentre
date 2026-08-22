package app

import (
	"errors"
	"fmt"

	"github.com/cago-frame/cago/pkg/utils/httputils"
)

/*
CodedErrorPrefix 是业务码过 wails 那座桥的**唯一**形状。

wails 把 error 序列化成 `err.Error()`，而 cago 的 `httputils.Error.Error()` 只返回
`Msg` —— 码在过桥时被丢掉了。前端拿到的只剩一句本地化文本，于是「不让看」与
「路径不存在」在界面上变成同一件事，而这两件事的出路完全不同：前者要去那台机器上
放开权限，后者要换一个目录。

**这是一条契约，不是靠文案反猜。** 反猜的做法（match 「权限」两个字）一改文案就静默
失灵，还得中英各猜一遍；这个前缀由两端各自的测试钉住，改它会当场变红。

对面在 `frontend/src/components/agentre/remote-fs-port.ts`。
*/
const CodedErrorPrefix = "agentre-code:"

// codedError 把带业务码的错误翻成一个前端解析得出码的错误。
//
// 没有码（普通 error、或 Code 为 0）就原样返回 —— 编一个码比不给码更糟。
func codedError(err error) error {
	if err == nil {
		return nil
	}
	var coded *httputils.Error
	if !errors.As(err, &coded) || coded.Code == 0 {
		return err
	}
	return fmt.Errorf("%s%d %s", CodedErrorPrefix, coded.Code, coded.Msg)
}
