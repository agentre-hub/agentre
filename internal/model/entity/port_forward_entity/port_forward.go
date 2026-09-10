// Package port_forward_entity 维护「设备端口转发映射」的实体：一条声明，即这台设备
// 允许把它 127.0.0.1 上的某个端口转出去（规格「映射的声明与生命周期」）。
//
// 映射存在**被访问的那台设备上**，两个宿主（桌面端 / agentred）各自一个 SQLite 库，
// 共用同一份实体与仓储代码（决策 1、与 transcript_entity 同一条路子）；对应的两条
// DDL 因此是复制关系而不是共享一张表，同形由 internal/daemon/migrations 下的
// parity 测试钉住。
//
// 规格「数据」一节写的字段是「端口、名称、启用位、时间戳」，并明写**没有所有者
// 标识**——本表因此不设那一列。本仓已有的设备侧表（daemon_sessions /
// chat_messages 等）核下来是同一条结论：它们的归属类列（peer_fingerprint / device_fingerprint，
// 见 docs/architecture.md「Device fingerprints」表的 Initiator / Carrier 两个角色）
// 都是在**一台设备的库里混着多个来源的行**时才需要——用来把「这一行是谁的」分开。
// 端口转发映射不是这种形状：规格明写「端口在一台设备下唯一」（不是「同一 owner 下
// 唯一」），且「任何一个有权连上这台设备的客户端都能列举、新增、启停与删除它」——
// 全部映射对该设备上的任何合法客户端都是同一份、不按来源拆分。表本身已经整张
// 落在这一台设备的库上，设备身份由「这是哪个库文件」回答，行内不需要再重复一遍；
// 凭空加一列不表达任何区分，只会是一列恒定值。
package port_forward_entity

// PortForward 是 port_forwards 表的一行：这台设备上的一条端口转发声明。
type PortForward struct {
	ID int64 `gorm:"column:id;primaryKey;autoIncrement"`
	// Port 是这台设备 127.0.0.1 上被声明转出去的端口。同一台设备下唯一
	// （库上的 UNIQUE 索引兜底，见迁移）。
	Port int `gorm:"column:port;type:int;not null"`
	// Name 是这条映射的辨认名称，用户在新增时填写。
	Name string `gorm:"column:name;type:text;not null;default:''"`
	// Enabled 是这条映射的启用开关。停用时声明保留，但访问一律拒绝
	// （规格「映射的声明与生命周期」）。
	//
	// **这一格不带 default 标注。** gorm 会把 default 当成「零值即未填」的替换值：
	// 带上 default:1，Create 一条 Enabled=false 的行会被就地改写成 true 再写出去
	// （连调用方手上那个结构体一起改），于是这张表根本插不进一条停用的声明，而且
	// 不报任何错。库上的 DEFAULT 1 留着就够了——那一条只在 INSERT 省掉这一列时生效，
	// 而本仓储每次都把它写全。
	Enabled    bool  `gorm:"column:enabled;type:boolean;not null"`
	Createtime int64 `gorm:"column:createtime;type:bigint;not null;default:0"`
	Updatetime int64 `gorm:"column:updatetime;type:bigint;not null;default:0"`
}

// TableName 绑定表名。
func (*PortForward) TableName() string { return "port_forwards" }
