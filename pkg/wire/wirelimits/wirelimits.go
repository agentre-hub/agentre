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

// MaxAttachmentBytes 是**一条消息里全部附件的原始字节总量**上限。
//
// 它从 MaxPayloadBytes 推导而不是另写一个字面量:上面那段注释记的正是「三处曾经
// 不同源」的教训,而超限的后果是整条物理连接被拆掉、那台机器上所有会话一起重连。
//
// 取三分之二的算法:附件在 runtime.run 里以 base64 过线,膨胀 4/3,所以 2/3 的原始
// 字节 base64 之后正好占满载荷的 8/9,余下的 1/9(约 1.16 MB)留给正文、提及、模型键
// 与其余字段。
//
// 它管的是**总量**这一维。单张附件的上限与张数上限是另外两件事,各自在产生方那一侧
// (共享包 composer 与 chat_svc)—— 一张 5 MB 的图合法,四张 5 MB 一起发不合法。
//
// 桌面端跑本机 runtime 是进程内调用,一个字节都不过 wire,这个上限在那条路上不是
// 事故防线;它照样生效,因为一条消息能带多少附件不该因为它这一轮恰好跑在哪儿而不同。
const MaxAttachmentBytes int64 = MaxPayloadBytes * 2 / 3
