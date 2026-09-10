// Package wirelimits 是 wire 协议的尺寸预算。
//
// 它刻意是一个**叶子**:不 import 本 module 里的任何别的包,于是传输层(daemon 的
// relaytransport)、RPC 层(protorpc)和服务端的中继端点可以各自取同一个数,而不必
// 有谁反向依赖谁。
package wirelimits

// MaxPayloadBytes 是一条 RPC **载荷**的上限,整条链路共用这一个数:桌面端 ↔ agentred
// 的直连、daemon 那条中继链路,以及浏览器接入服务端的那一跳。
//
// 三处曾经不同源(直连与 daemon 侧 16 MiB,服务端 10 MiB)。后果不是「大一点的请求
// 失败了」—— 超限时 gorilla 回 1009 并让读循环出错,于是**整条物理连接**被拆掉,而
// daemon 那条链路上跑着那台机器的全部虚拟通道,所有会话一起断线重连。
//
// 取小的那个数:中继上跑的是别的设备发来的字节,不是本机可信输入。
//
// 链路的读上限还要再加一个信封头(relayenvelope.MaxEnvelopeBytes)—— 中继上收到的
// 每一帧都是套过信封的载荷。
const MaxPayloadBytes int64 = 10 << 20
