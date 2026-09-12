package portforward

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/port_forward_entity"
	"github.com/agentre-hub/agentre/internal/repository/port_forward_repo"
	"github.com/agentre-hub/agentre/internal/repository/port_forward_repo/mock_port_forward_repo"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// 本文件测的是**设备侧那一份判定**:声明族的四个方法,以及 open 的授权闸门。
// 仓储一律走 mockgen 注入的 mock,不连库(AGENTS.md「Task-specific hard boundaries」)。

func setup(t *testing.T, dial Dialer) (context.Context, *mock_port_forward_repo.MockPortForwardRepo, *Handlers) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	repo := mock_port_forward_repo.NewMockPortForwardRepo(ctrl)
	return context.Background(), repo, NewHandlers(Options{Repo: repo, Dial: dial})
}

// refusingDialer 是一个**按下去就判红**的拨号器。它存在是因为「拒绝得对」不等于
// 「拒绝得够早」:先拨出去再拒,错误码照样是对的,而那台设备上的服务已经收到了一次
// 来自未授权端口的连接 —— 规格「设备侧的目标限制」要的是落地前被拒。
func refusingDialer(t *testing.T) Dialer {
	t.Helper()
	return func(_ context.Context, port int) (net.Conn, error) {
		t.Fatalf("判定之前不该拨号,却拨了 127.0.0.1:%d", port)
		return nil, nil
	}
}

func code(t *testing.T, err error) int32 {
	t.Helper()
	var rpcErr *rpcerror.Error
	require.ErrorAs(t, err, &rpcErr, "设备侧的拒绝必须带领域错误码,调用方按码分支")
	return rpcErr.Code
}

// Given 一个从没在这台设备上声明过的端口,When 有人开转发流,Then 在任何拨号动作
// 之前被拒,回 NotDeclared。
func TestOpen_GivenAnUndeclaredPort_WhenOpening_ThenRefusedBeforeAnyDial(t *testing.T) {
	ctx, repo, handlers := setup(t, refusingDialer(t))
	repo.EXPECT().FindByPort(gomock.Any(), 3000).Return(nil, nil)

	conn, err := handlers.DialDeclared(ctx, 3000)

	assert.Nil(t, conn)
	assert.Equal(t, int32(rpcerror.CodePortForwardNotDeclared), code(t, err))
}

// Given 一条还在、但被停用的声明,When 有人开转发流,Then 回 Disabled 而不是
// NotDeclared —— 两种失败用户要做的事不同:一个是把开关打开,一个是重新建一条。
func TestOpen_GivenADisabledMapping_WhenOpening_ThenRefusedAsDisabled(t *testing.T) {
	ctx, repo, handlers := setup(t, refusingDialer(t))
	repo.EXPECT().FindByPort(gomock.Any(), 3000).
		Return(&port_forward_entity.PortForward{ID: 7, Port: 3000, Enabled: false}, nil)

	conn, err := handlers.DialDeclared(ctx, 3000)

	assert.Nil(t, conn)
	assert.Equal(t, int32(rpcerror.CodePortForwardDisabled), code(t, err),
		"停用的声明不能折进 NotDeclared:界面据此提示「把它打开」而不是「重新建一条」")
}

// Given 一条已声明且启用的映射,When 开转发流,Then 判定放行,拨号只发生在这之后,
// 且**只带得出端口** —— 目标主机恒为环回,不由调用方指定(规格「设备侧的目标限制」)。
func TestOpen_GivenADeclaredEnabledMapping_WhenOpening_ThenDialsThatPortOnly(t *testing.T) {
	dialed := 0
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	ctx, repo, handlers := setup(t, func(_ context.Context, port int) (net.Conn, error) {
		dialed = port
		return client, nil
	})
	repo.EXPECT().FindByPort(gomock.Any(), 5173).
		Return(&port_forward_entity.PortForward{ID: 9, Port: 5173, Enabled: true}, nil)

	conn, err := handlers.DialDeclared(ctx, 5173)

	require.NoError(t, err)
	assert.Same(t, client, conn)
	assert.Equal(t, 5173, dialed, "拨号只带得出端口:目标主机恒为环回,不由调用方指定")
}

// Given 端口过了声明集判定、但那台设备上没有服务在监听,When 开转发流,Then 回
// NoListener —— 与「没声明」分开:一个要去把服务起起来,一个要先建声明。
func TestOpen_GivenNothingListening_WhenOpening_ThenNoListener(t *testing.T) {
	ctx, repo, handlers := setup(t, func(context.Context, int) (net.Conn, error) {
		return nil, errors.New("dial tcp 127.0.0.1:3000: connect: connection refused")
	})
	repo.EXPECT().FindByPort(gomock.Any(), 3000).
		Return(&port_forward_entity.PortForward{ID: 7, Port: 3000, Enabled: true}, nil)

	_, err := handlers.DialDeclared(ctx, 3000)

	assert.Equal(t, int32(rpcerror.CodePortForwardNoListener), code(t, err))
}

// 库读不出来时不能当作「没声明」:那会把一次故障说成一条用户能自己改正的输入错误。
func TestOpen_GivenTheRepoFails_WhenOpening_ThenInternalNotNotDeclared(t *testing.T) {
	ctx, repo, handlers := setup(t, refusingDialer(t))
	repo.EXPECT().FindByPort(gomock.Any(), 3000).Return(nil, errors.New("db down"))

	_, err := handlers.DialDeclared(ctx, 3000)

	assert.Equal(t, rpcerror.CodeInternal, code(t, err))
}

// Given 这台设备上已有两条声明,When 客户端列举,Then 每一格都按线上形状交出来。
// 时间戳在库里是毫秒(仓储写的是 UnixMilli),线上那一格是 unix 秒 —— 换算收在这一
// 处边界上,而不是让每个消费方各猜一次。
func TestList_GivenStoredMappings_WhenListing_ThenEveryFieldIsMapped(t *testing.T) {
	ctx, repo, handlers := setup(t, nil)
	repo.EXPECT().List(gomock.Any()).Return([]*port_forward_entity.PortForward{
		{ID: 1, Port: 3000, Name: "dev server", Enabled: true, Createtime: 1757300000123, Updatetime: 1757300009123},
		{ID: 2, Port: 5173, Name: "vite", Enabled: false, Createtime: 1757300001000, Updatetime: 1757300001000},
	}, nil)

	resp, err := handlers.List(ctx, &agentrewire.PortForwardListRequest{})

	require.NoError(t, err)
	require.Len(t, resp.GetMappings(), 2)
	assert.Equal(t, int64(1), resp.GetMappings()[0].GetId())
	assert.Equal(t, uint32(3000), resp.GetMappings()[0].GetPort())
	assert.Equal(t, "dev server", resp.GetMappings()[0].GetName())
	assert.True(t, resp.GetMappings()[0].GetEnabled())
	assert.Equal(t, int64(1757300000), resp.GetMappings()[0].GetCreatetime())
	assert.Equal(t, int64(1757300009), resp.GetMappings()[0].GetUpdatetime())
	assert.False(t, resp.GetMappings()[1].GetEnabled())
}

// Given 一个还没被声明过的端口,When 新增,Then 落库并把**设备定下的那一行**交回来
// (id、启用位、时间戳都由设备定,调用方不猜)。
func TestCreate_GivenAFreePort_WhenCreating_ThenTheStoredRowComesBack(t *testing.T) {
	ctx, repo, handlers := setup(t, nil)
	repo.EXPECT().FindByPort(gomock.Any(), 3000).Return(nil, nil)
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, row *port_forward_entity.PortForward) error {
			assert.Equal(t, 3000, row.Port)
			assert.Equal(t, "dev server", row.Name)
			assert.True(t, row.Enabled, "新建的声明默认启用:用户刚填完就是要用它")
			row.ID = 11
			row.Createtime, row.Updatetime = 1757300000123, 1757300000123
			return nil
		})

	resp, err := handlers.Create(ctx, &agentrewire.PortForwardCreateRequest{Port: 3000, Name: "dev server"})

	require.NoError(t, err)
	assert.Equal(t, int64(11), resp.GetMapping().GetId())
	assert.Equal(t, uint32(3000), resp.GetMapping().GetPort())
	assert.True(t, resp.GetMapping().GetEnabled())
	assert.Equal(t, int64(1757300000), resp.GetMapping().GetCreatetime())
}

// Given 这个端口已经声明过,When 再新增一条,Then 回 PortTaken —— 端口在一台设备下
// 唯一,这是一次可以就地改正的输入错误,不是写失败。
func TestCreate_GivenThePortIsAlreadyDeclared_WhenCreating_ThenPortTaken(t *testing.T) {
	ctx, repo, handlers := setup(t, nil)
	repo.EXPECT().FindByPort(gomock.Any(), 3000).
		Return(&port_forward_entity.PortForward{ID: 7, Port: 3000, Enabled: true}, nil)

	_, err := handlers.Create(ctx, &agentrewire.PortForwardCreateRequest{Port: 3000, Name: "again"})

	assert.Equal(t, int32(rpcerror.CodePortForwardPortTaken), code(t, err))
}

// 预检与库上的 UNIQUE 索引之间永远有一条竞态窗口(仓储包注释明写唯一性的真相源是库
// 本身)。窗口里挤进来的那一次必须落在**同一个码**上,否则同一件事在两条路径上会被
// 说成两句话:一句「端口已被占用」,一句 -32603。
func TestCreate_GivenTheUniqueIndexRejectsIt_WhenCreating_ThenStillPortTaken(t *testing.T) {
	ctx, repo, handlers := setup(t, nil)
	repo.EXPECT().FindByPort(gomock.Any(), 3000).Return(nil, nil)
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).
		Return(errors.New("constraint failed: UNIQUE constraint failed: port_forwards.port (2067)"))

	_, err := handlers.Create(ctx, &agentrewire.PortForwardCreateRequest{Port: 3000, Name: "racing"})

	assert.Equal(t, int32(rpcerror.CodePortForwardPortTaken), code(t, err))
}

// 端口号不在 1..65535 内的连库都不该碰。
func TestCreate_GivenAPortOutsideTheValidRange_WhenCreating_ThenInvalidPort(t *testing.T) {
	for name, port := range map[string]uint32{"零": 0, "越界": 70000} {
		t.Run(name, func(t *testing.T) {
			ctx, _, handlers := setup(t, nil)

			_, err := handlers.Create(ctx, &agentrewire.PortForwardCreateRequest{Port: port, Name: "x"})

			assert.Equal(t, int32(rpcerror.CodePortForwardInvalidPort), code(t, err))
		})
	}
}

// Given 一条已有的声明,When 启停它,Then 交回改过之后的那一行。
func TestSetEnabled_GivenAnExistingMapping_WhenToggling_ThenTheUpdatedRowComesBack(t *testing.T) {
	ctx, repo, handlers := setup(t, nil)
	repo.EXPECT().SetEnabled(gomock.Any(), int64(7), false).Return(int64(1), nil)
	repo.EXPECT().Get(gomock.Any(), int64(7)).
		Return(&port_forward_entity.PortForward{ID: 7, Port: 3000, Name: "dev", Enabled: false, Updatetime: 1757300009123}, nil)

	resp, err := handlers.SetEnabled(ctx, &agentrewire.PortForwardSetEnabledRequest{Id: 7, Enabled: false})

	require.NoError(t, err)
	assert.False(t, resp.GetMapping().GetEnabled())
	assert.Equal(t, int64(7), resp.GetMapping().GetId())
}

// Given 一条已经不在了的声明(另一个客户端刚删掉),When 启停它,Then 回 NotDeclared
// —— 这与「删除幂等」不冲突:启停要改的那一行确实不存在,调用方的列表落后了一步。
func TestSetEnabled_GivenTheMappingIsGone_WhenToggling_ThenNotDeclared(t *testing.T) {
	ctx, repo, handlers := setup(t, nil)
	repo.EXPECT().SetEnabled(gomock.Any(), int64(7), true).Return(int64(0), nil)

	_, err := handlers.SetEnabled(ctx, &agentrewire.PortForwardSetEnabledRequest{Id: 7, Enabled: true})

	assert.Equal(t, int32(rpcerror.CodePortForwardNotDeclared), code(t, err))
}

// 删除保持幂等:删掉了就是 true,本来就没有就是 false,两次都不是错误。
func TestDelete_GivenAMapping_WhenDeleting_ThenDeletedReportsWhetherARowWentAway(t *testing.T) {
	for name, tc := range map[string]struct {
		rows int64
		want bool
	}{"删掉了": {1, true}, "本来就没有": {0, false}} {
		t.Run(name, func(t *testing.T) {
			ctx, repo, handlers := setup(t, nil)
			// 删除前先回读一次:撤销面按端口认流,而行一删掉就再问不出端口是多少
			// (revoke_test.go 的两条用例钉住这一步的用途)。
			var row *port_forward_entity.PortForward
			if tc.rows > 0 {
				row = &port_forward_entity.PortForward{ID: 7, Port: 3000, Enabled: true}
			}
			repo.EXPECT().Get(gomock.Any(), int64(7)).Return(row, nil)
			repo.EXPECT().Delete(gomock.Any(), int64(7)).Return(tc.rows, nil)

			resp, err := handlers.Delete(ctx, &agentrewire.PortForwardDeleteRequest{Id: 7})

			require.NoError(t, err)
			assert.Equal(t, tc.want, resp.GetDeleted())
		})
	}
}

// Handlers 必须真的满足仓储接口所需的注入面:两个宿主各自把自己的库交进来。
var _ port_forward_repo.PortForwardRepo = (*mock_port_forward_repo.MockPortForwardRepo)(nil)
