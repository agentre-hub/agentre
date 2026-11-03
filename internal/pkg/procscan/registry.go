package procscan

import "sync"

// 宿主当前作为 CLI 启动、仍在运行的进程。进程全局，因为登记方（runtime）
// 与扫描方（Scanner）互不持有对方。
var (
	cliMu   sync.Mutex
	cliPIDs = map[int]int{} // pid -> 登记次数
)

// TrackCLI 登记一个 CLI 进程 pid，返回的 release 幂等，进程退出时调用。
func TrackCLI(pid int) (release func()) {
	cliMu.Lock()
	cliPIDs[pid]++
	cliMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			cliMu.Lock()
			defer cliMu.Unlock()
			if cliPIDs[pid] <= 1 {
				delete(cliPIDs, pid)
				return
			}
			cliPIDs[pid]--
		})
	}
}

// trackedCLIs 返回登记表的快照，调用方可随意修改。
func trackedCLIs() map[int]struct{} {
	cliMu.Lock()
	defer cliMu.Unlock()
	out := make(map[int]struct{}, len(cliPIDs))
	for pid := range cliPIDs {
		out[pid] = struct{}{}
	}
	return out
}
