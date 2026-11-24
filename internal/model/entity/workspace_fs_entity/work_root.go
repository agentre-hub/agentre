// Package workspace_fs_entity 存放工作根认领的持久化实体。
package workspace_fs_entity

// WorkRoot 是一个会话认领到的、会话 cwd 之外的工作根(session_work_roots)。
// 会话 cwd 本身是主根,恒在集合里,不入表。
//
// 同一会话的同一路径只记一行。Worktree 是该工作树在会话主仓库 git 公共目录下的
// 短名(主 checkout 为空串):公共目录对一个会话的所有根都相同,短名在其中唯一,
// 因此 (会话, Worktree) 就是「这个工作树是否已认领」的身份,不受路径写法影响。
type WorkRoot struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement"`
	SessionID  int64  `gorm:"column:session_id;type:integer;not null"`
	Path       string `gorm:"column:path;type:text;not null"`
	Worktree   string `gorm:"column:worktree;type:text;not null;default:''"`
	Createtime int64  `gorm:"column:createtime;type:bigint;not null;default:0"`
}

func (WorkRoot) TableName() string { return "session_work_roots" }
