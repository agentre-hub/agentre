package app

import (
	"github.com/agentre-hub/agentre/internal/service/remote_fs_svc"
)

/*
远端文件系统这两个绑定的错误**带着业务码过桥**（见 coded_error.go）。

目录选择器要把八种失败分开说（不让看 / 不存在 / 不是目录 / 被拒 / 重名 / 名字非法 /
掉线 / 说不清），而这几件事的出路各不相同。只交一句本地化文本的话，前端只能把它们
折成同一句「读不到这个目录」——那正是这一层此前的样子。
*/

// RemoteFsListDir 列出远端 device 上某目录下的子项。
//   - deviceID = paired_agentred.id 字符串化(与 ProjectLocationUpsert 一致)
//   - path 为空 → daemon 端解析为 $HOME
func (a *App) RemoteFsListDir(deviceID, path string) (*remote_fs_svc.ListDirView, error) {
	view, err := remote_fs_svc.Default().ListDir(a.ctx, deviceID, path)
	if err != nil {
		return nil, codedError(err)
	}
	return view, nil
}

// RemoteFsMkdir 在远端 device 上的 parent 下创建文件夹 name(非递归)。
func (a *App) RemoteFsMkdir(deviceID, parent, name string) (*remote_fs_svc.MkdirView, error) {
	view, err := remote_fs_svc.Default().Mkdir(a.ctx, deviceID, parent, name)
	if err != nil {
		return nil, codedError(err)
	}
	return view, nil
}
