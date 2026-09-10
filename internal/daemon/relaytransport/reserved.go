package relaytransport

// ReservedChannelPrefix 与 SignalChannelID 必须与 agentre-server 的
// relay_svc.ReservedChannelPrefix / relay_svc.SignalChannelID 逐字同值——两个仓库
// 各自维护一份常量而不是共享一个包，因为 Go 后端之间不允许反向 import（AGENTS.md）。
// 决策 14：保留号取一个不在通道 id 字母表内的字符作前缀。通道 id 两端各自生成：
// server 侧是 base64url（relay_svc.newChannelID），daemon 侧与本包的 Open() 一样
// 是 hex（newRelayChannelID）。两套字母表都不含 "~"，因此以 "~" 开头的保留号由
// 构造不可能与随机分配的通道相撞（见 reserved_test.go 的 TestNewRelayChannelID_
// NeverCollidesWithTheReservedPrefix）。
const (
	ReservedChannelPrefix = "~"
	// SignalChannelID 是账号信号那条保留通道（决策 13）。它只出不进：服务端开、
	// 服务端写；agentred 一侧收到时要把它交给信号处理器而非 RPC 注册表（daemon.go
	// 的 serveRelayChannels）；desktop 一侧的中继客户端连接同理（server_svc）。
	SignalChannelID = ReservedChannelPrefix + "signal"
)
