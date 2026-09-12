// Package sync_account_entity 维护「本机认得的账号」这张本地表。
//
// 它存在的理由只有一条：**账号主键在跨 server 时不唯一。** server 的 user_id 是它
// 自己库里的自增主键，两套自建部署的第一个用户都是 1。而同步侧的一切归属判定
// （行属于谁、队列属于谁、游标属于谁）落在 sync_account_id 这一个整数上——光存
// server 的 user_id，换一套 server 之后本机会把 B 的 1 号用户认成 A 的 1 号用户，
// 上一个账号的行因此照常上行到新 server 里去。
//
// 所以 sync_account_id 存的不再是 server 的 user_id，而是本表的主键：本机给
// (server 地址, 远端用户主键) 这一对分配的代理键。同一对永远拿到同一个键，不同对
// 永远不同。server 侧一无所知，这是纯本地的一层身份。
package sync_account_entity

// SyncAccount 是本机为「某一套 server 上的某个账号」分配的一行。
type SyncAccount struct {
	// ID 就是同步组各表 sync_account_id 里存的那个值。
	ID int64 `gorm:"column:id;primaryKey;autoIncrement"`
	// ServerURL 是归一化之后的 server 地址（去掉首尾空白与末尾斜杠）。
	ServerURL string `gorm:"column:server_url;type:text;not null;default:''"`
	// RemoteUserID 是这套 server 自己库里的用户主键。它只在同一个 ServerURL 下有
	// 意义，绝不单独用作身份。
	RemoteUserID int64 `gorm:"column:remote_user_id;type:integer;not null;default:0"`
	Createtime   int64 `gorm:"column:createtime;type:integer;not null;default:0"`
}

// TableName GORM 表名。
func (s *SyncAccount) TableName() string { return "sync_accounts" }
