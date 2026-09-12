// Package transcript 是「事件 → 块」的累积与「块 → 帧」的投影这一层的唯一实现，
// 桌面端（chat_svc）与 agentred 共用一份（决策 2）。先例：internal/pkg/turnstats
// 的注释即「与 chat_svc 共用一份」。
//
// 这里有什么：
//
//   - NewTurnDispatcher —— 事件类型 → handler 的注册表。子包 turn 提供累积器与
//     路由骨架，handlers 提供各类型 handler，blocks 提供它们落下的块类型。
//   - ProjectMessages / EventForStoredBlock —— 把落库的消息摊成对端读得到的持久帧，
//     含认不出的块走 agentruntime.UnrecognizedBlock 的兜底。
//
// 这里**没有**什么（宿主各自持有，也不应共用）：会话行与会话生命周期、持久化适配器
// （usage / error / context window / permission mode / plan 的写入）、发射器
// （Wails 事件 vs RPC 通知）。它们通过 Adapters 与 turn.TurnContext 进入本包。
//
// 判据：同一段内容在两个宿主上必须由同一行代码写进库、由同一行代码折成帧。
// 出现第二份累积或第二份块 → 帧投影由 duplicate_guard_test.go 判红。
package transcript
