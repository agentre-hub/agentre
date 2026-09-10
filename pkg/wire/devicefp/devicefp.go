// Package devicefp 给设备指纹的四个角色各一个类型。
//
// 设备指纹是一台机器的稳定身份 —— 由 daemon 的 instance UUID 派生出的
// `sha256:<hex>` 串。整个工作区所有指纹列都取自这一个值空间：它们彼此逐字节比较，
// 也与 server 上的 `devices.fingerprint` 逐字节比较（server 因此把它们都钉在
// `utf8mb4_0900_bin` 上，字节精确且 NO PAD）。
//
// 一个值空间，但有四个真正不同的**角色**（见 docs/architecture.md 的
// 「Device fingerprints: one concept, four roles, one name per role」）：
//
//	角色                        它回答什么                      规范列名
//	Carrier    承载者          这东西跑在/属于哪台机器？        device_fingerprint
//	Initiator  发起方          是哪一端把这份活交出来的？        peer_fingerprint
//	LastWriter 最后写者        哪台机器做了最近这次修改？        origin_fingerprint
//	Client     待授权客户端    哪台机器在请求被授权？            client_fingerprint
//
// 承载者与发起方的区别最咬人：在 agent_session_saves 上它们是同一张表的两列 ——
// 一段从 web 控制台派发出去的会话，由浏览器的中继身份**发起**，由某台 agentred
// **承载**，而那个浏览器压根不在账号的设备列表里。
//
// # 为什么要四个类型而不是一个 string
//
// 混用两个角色是真 bug，不是风格问题：两边都是 string，所以编译器和数据库都不会
// 拦你。已经发生过 —— dev 上的 be367651「远端 Agent 加不进项目:设备指纹与配对
// 数字 id 被当成同一个键比」就是这一类。这四个是**定义类型**（defined type），
// 按 Go 的赋值规则两两不可赋值、也不能与裸 string 互相赋值，于是同一类误用现在
// 编译不过。
//
// # 边界
//
// protobuf 生成的是裸 string，数据库列也是。那正是要显式说出角色的地方：在
// repo/entity ↔ svc、以及 wire ↔ handler 这两道边界上各转一次，转换点少而集中，
// 不要在每个调用点各转一次。同一个值换角色（对端交过来的 Initiator，落到本机
// 就是那台机器的 Carrier）同样写一次显式转换 —— 那是有意的语义跳变，应当在
// diff 里看得见。
//
// # 它为什么住在这个 module
//
// 这些值要跨仓库比较（与 server 的 devices.fingerprint 逐字节相等），所以类型
// 得放在两个仓库都能 import 的地方。pkg/wire 是 agentre-server 已经钉住的那个
// 共享 module，且指纹本身就是这条协议的词汇：wire.proto 里既有
// device_fingerprint 也有 peer_fingerprint。本包零依赖，不 import 任何宿主代码。
//
// 四个角色都在这里定义，但桌面仓只用得到其中三个：client_fingerprint 那一列在
// 本仓一处也没有（它在 agentre-server 那侧），所以 Client 现在没有使用者。它照样
// 留在这里而不是等到用得上时再加 —— 这个包的意义就是「一个角色一个名字」，缺一个
// 就等于把那个角色留给下一个人另起炉灶。
package devicefp

// Carrier 承载者：这东西跑在/属于哪台机器。规范列名 device_fingerprint。
//
// 冻结的别名列 agentred_fingerprint / daemon_fingerprint / machine_fingerprint
// 表达的都是这个角色 —— 列名跨着已发布的线格式不能改，但读出来的 Go 值当然是
// Carrier。
type Carrier string

// Initiator 发起方：是哪一端把这份活交出来的。规范列名 peer_fingerprint。
//
// 它不一定在账号的设备列表里（web 控制台的中继身份就不在），所以拿 Initiator
// 去配对表里解析名字/在线状态是错的 —— 那是 Carrier 才能回答的问题。
type Initiator string

// LastWriter 最后写者：哪台机器做了最近这次修改。规范列名 origin_fingerprint。
//
// 冻结的别名列 sync_origin_fingerprint 是它在桌面端这一侧的对应物。
type LastWriter string

// Client 待授权客户端：哪台机器在请求被授权。规范列名 client_fingerprint。
//
// 本仓当前没有使用者：这一列住在 agentre-server 那侧。桌面端的配对/账号登录握手
// 出示的是本机身份，可 wire.proto 上那两处（AuthPairRequest / AuthConnectRequest
// 的 device_fingerprint）用的是**承载者**的列名而不是这个角色的，那是一条已发布
// 的线格式，不在这一轮里重新归类。
type Client string
