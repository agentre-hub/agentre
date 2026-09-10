// Package port_forward_repo 提供「设备端口转发映射」的持久化访问：新增、列举、
// 启停、删除，以及按端口的点查。两个宿主（桌面端 / agentred）各自一个 SQLite 库，
// 按 db.Ctx(ctx) 写，共用同一份实现（决策 1，与 transcript_repo 同一条路子）。
//
// 「端口在一台设备下唯一」由库上的 UNIQUE 索引兜底（迁移里的
// ux_port_forwards_port），本包的 Create 不重复做应用层预检——预检与约束之间永远
// 有一条竞态窗口，唯一性的真相源只能是库本身；本包只保证违反时把底层错误原样交回
// 调用方，不吞掉它。
package port_forward_repo

import (
	"context"
	"errors"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/port_forward_entity"
)

//go:generate mockgen -source port_forward.go -destination mock_port_forward_repo/mock_port_forward.go

// PortForwardRepo 存取这台设备上的端口转发声明。
type PortForwardRepo interface {
	// Create 新增一条映射声明，写下建行 / 更新时间。端口冲突时把库上 UNIQUE 索引
	// 报出的错误原样交回，不吞掉、也不改写成另一种含糊的失败。
	Create(ctx context.Context, p *port_forward_entity.PortForward) error

	// Get 按主键取一条映射，不存在返回 (nil, nil)。
	Get(ctx context.Context, id int64) (*port_forward_entity.PortForward, error)

	// FindByPort 按端口点查这台设备上的声明，不存在返回 (nil, nil)。设备侧 open
	// 判定（端口是否在声明集内）与新增前的应用层提示都走它。
	FindByPort(ctx context.Context, port int) (*port_forward_entity.PortForward, error)

	// List 列出这台设备上的全部映射，按 id 升序（声明顺序）。不按来源收窄——
	// 规格「任何一个有权连上这台设备的客户端都能列举...改动落在那台设备上，
	// 另一个客户端下次列举就看得到」。
	List(ctx context.Context) ([]*port_forward_entity.PortForward, error)

	// SetEnabled 启停一条映射，返回受影响行数（0 = 没有这一行）。声明本身保留，
	// 只改 enabled 一列。
	SetEnabled(ctx context.Context, id int64, enabled bool) (int64, error)

	// Delete 删除一条映射，返回受影响行数（0 = 没有这一行，删除保持幂等）。
	Delete(ctx context.Context, id int64) (int64, error)
}

// NewPortForward 构造默认 GORM 实现。
//
// 本包**不设**包级单例与 Register 注入口：这份仓储的两个消费者（agentred 的
// daemon.New、桌面端的 peer.NewInbound）都是把它构造出来直接注进
// portforward.Options.Repo 的，依赖走参数而不是全局。留一个没人注册的
// accessor 只会是一个恒返回 nil 的陷阱。
func NewPortForward() PortForwardRepo { return &portForwardRepo{} }

type portForwardRepo struct{}

func (r *portForwardRepo) Create(ctx context.Context, p *port_forward_entity.PortForward) error {
	now := time.Now().UnixMilli()
	if p.Createtime == 0 {
		p.Createtime = now
	}
	p.Updatetime = now
	return db.Ctx(ctx).Create(p).Error
}

func (r *portForwardRepo) Get(ctx context.Context, id int64) (*port_forward_entity.PortForward, error) {
	row := &port_forward_entity.PortForward{}
	err := db.Ctx(ctx).Where("id = ?", id).First(row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *portForwardRepo) FindByPort(ctx context.Context, port int) (*port_forward_entity.PortForward, error) {
	row := &port_forward_entity.PortForward{}
	err := db.Ctx(ctx).Where("port = ?", port).First(row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *portForwardRepo) List(ctx context.Context) ([]*port_forward_entity.PortForward, error) {
	var rows []*port_forward_entity.PortForward
	if err := db.Ctx(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *portForwardRepo) SetEnabled(ctx context.Context, id int64, enabled bool) (int64, error) {
	res := db.Ctx(ctx).Model(&port_forward_entity.PortForward{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"enabled":    enabled,
			"updatetime": time.Now().UnixMilli(),
		})
	return res.RowsAffected, res.Error
}

func (r *portForwardRepo) Delete(ctx context.Context, id int64) (int64, error) {
	res := db.Ctx(ctx).Where("id = ?", id).Delete(&port_forward_entity.PortForward{})
	return res.RowsAffected, res.Error
}
