package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-ai/agentre/internal/pkg/agentskill"
)

// fakeSkillDisc 替身发现器:记录入参,回预置包,免依赖真实 claude 二进制。
type fakeSkillDisc struct {
	gotQuery agentskill.DiscoverQuery
	packs    []agentskill.SkillPack
}

func (f *fakeSkillDisc) Discover(_ context.Context, q agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	f.gotQuery = q
	return f.packs, nil
}

func TestSkillsHandler_List_RunsDaemonDiscoverer(t *testing.T) {
	fd := &fakeSkillDisc{packs: []agentskill.SkillPack{
		{ID: "superpowers@claude-plugins-official", Name: "superpowers", Installed: true, Source: agentskill.SourceInstalled, GloballyEnabled: true},
	}}
	restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, fd)
	t.Cleanup(restore)

	// daemon 自己解析本机 CLI 路径喂给发现器(desktop 不知道 daemon 的 claude 在哪)。
	SetResolveCLIPathFunc(func(bt string) (string, bool, error) {
		require.Equal(t, "claudecode", bt)
		return "/daemon/bin/claude", true, nil
	})
	t.Cleanup(ResetResolveCLIPathFunc)

	h := NewSkillsHandlers()
	res, err := h.List(context.Background(), SkillsListParams{BackendType: "claudecode"})
	require.NoError(t, err)
	require.Len(t, res.Packs, 1)
	require.Equal(t, "superpowers@claude-plugins-official", res.Packs[0].ID)
	require.True(t, res.Packs[0].GloballyEnabled)
	require.Equal(t, "/daemon/bin/claude", fd.gotQuery.CLIPath)
	require.Equal(t, agent_backend_entity.TypeClaudeCode, fd.gotQuery.BackendType)
}

func TestSkillsHandler_List_ExplicitCLIPathWins(t *testing.T) {
	fd := &fakeSkillDisc{}
	restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, fd)
	t.Cleanup(restore)
	// 显式传 CLIPath 时直接用,不再走本机解析。
	SetResolveCLIPathFunc(func(string) (string, bool, error) {
		t.Fatal("must not resolve when CLIPath provided")
		return "", false, nil
	})
	t.Cleanup(ResetResolveCLIPathFunc)

	h := NewSkillsHandlers()
	_, err := h.List(context.Background(), SkillsListParams{BackendType: "claudecode", CLIPath: "/custom/claude"})
	require.NoError(t, err)
	require.Equal(t, "/custom/claude", fd.gotQuery.CLIPath)
}

func TestSkillsHandler_List_NoDiscovererReturnsEmpty(t *testing.T) {
	h := NewSkillsHandlers()
	res, err := h.List(context.Background(), SkillsListParams{BackendType: "nonesuch"})
	require.NoError(t, err)
	require.NotNil(t, res.Packs, "Packs 永远非 nil,序列化给 desktop 是空数组而非 null")
	require.Empty(t, res.Packs)
}

// errSkillDisc 替身发现器:枚举失败(CLI 在,但 `claude plugin list` 报错)。
type errSkillDisc struct{ err error }

func (f errSkillDisc) Discover(context.Context, agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	return nil, f.err
}

// TestSkillsHandler_Catalog_MergesRecommendedInstalledAndAuthorized 是浏览器要画的
// 那份目录:这台机器上装了什么 + agentre 推荐什么 + 这一档授权了什么,合并成一张表。
func TestSkillsHandler_Catalog_MergesRecommendedInstalledAndAuthorized(t *testing.T) {
	fd := &fakeSkillDisc{packs: []agentskill.SkillPack{
		{
			ID: "installed@mine", Name: "installed", Description: "装在这台机器上",
			Skills: []string{"a", "b"}, Installed: true, GloballyEnabled: true,
			Source: agentskill.SourceInstalled,
		},
	}}
	restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, fd)
	t.Cleanup(restore)
	SetResolveCLIPathFunc(func(string) (string, bool, error) { return "/daemon/bin/claude", true, nil })
	t.Cleanup(ResetResolveCLIPathFunc)

	h := NewSkillsHandlers()
	res, err := h.Catalog(context.Background(), wire.SkillCatalogParams{
		BackendType: "claudecode",
		Authorized:  []wire.SkillAuthorization{{ID: "installed@mine", Enabled: true}},
	})
	require.NoError(t, err)
	require.Equal(t, wire.SkillDiscoveryOK, res.Discovery)
	require.Equal(t, "/daemon/bin/claude", fd.gotQuery.CLIPath)

	byID := map[string]wire.SkillPackSummary{}
	for _, p := range res.Packs {
		byID[p.ID] = p
	}
	got := byID["installed@mine"]
	require.Equal(t, "installed", got.Name)
	require.Equal(t, "装在这台机器上", got.Description)
	require.Equal(t, []string{"a", "b"}, got.Skills)
	require.True(t, got.Installed)
	require.True(t, got.Enabled, "请求里带的授权必须落到这一行上")
	require.True(t, got.GloballyEnabled)

	// agentre 的推荐包也在目录里(未装),浏览器据此画出「可安装」那一组。
	require.NotEmpty(t, agentskill.RecommendedFor(agent_backend_entity.TypeClaudeCode))
	rec := agentskill.RecommendedFor(agent_backend_entity.TypeClaudeCode)[0]
	require.Contains(t, byID, rec.ID)
	require.False(t, byID[rec.ID].Installed)
	require.False(t, byID[rec.ID].Enabled)
}

// TestSkillsHandler_Catalog_UnauthorizedPacksAreNotEnabled 钉住 R15e「一档一块」在
// 协议上的表达:执行端只照请求里那一份授权标注,不会替调用方补出别的档的授权。
func TestSkillsHandler_Catalog_UnauthorizedPacksAreNotEnabled(t *testing.T) {
	fd := &fakeSkillDisc{packs: []agentskill.SkillPack{
		{ID: "installed@mine", Name: "installed", Installed: true, GloballyEnabled: true},
	}}
	restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, fd)
	t.Cleanup(restore)
	SetResolveCLIPathFunc(func(string) (string, bool, error) { return "/daemon/bin/claude", true, nil })
	t.Cleanup(ResetResolveCLIPathFunc)

	h := NewSkillsHandlers()
	res, err := h.Catalog(context.Background(), wire.SkillCatalogParams{BackendType: "claudecode"})
	require.NoError(t, err)
	for _, p := range res.Packs {
		require.False(t, p.Enabled, "没带授权就一行都不该是已授权:%s", p.ID)
	}
}

// TestSkillsHandler_Catalog_DiscoveryFailureIsNotAnEmptyCatalog 是本方法最要紧的一条。
// 枚举失败时回空目录且不说明,浏览器就会把它读成「这台机器上没有技能包」并请用户去
// 装 —— 而真相是这台机器此刻答不出来。
func TestSkillsHandler_Catalog_DiscoveryFailureIsNotAnEmptyCatalog(t *testing.T) {
	restore := agentskill.SwapDiscovererForTest(
		agent_backend_entity.TypeClaudeCode, errSkillDisc{err: errors.New("plugin list exited 1")})
	t.Cleanup(restore)
	SetResolveCLIPathFunc(func(string) (string, bool, error) { return "/daemon/bin/claude", true, nil })
	t.Cleanup(ResetResolveCLIPathFunc)

	h := NewSkillsHandlers()
	res, err := h.Catalog(context.Background(), wire.SkillCatalogParams{BackendType: "claudecode"})
	require.NoError(t, err, "答不出不是调用失败:已授权的仍要能移除,界面不该整块报错")
	require.Equal(t, wire.SkillDiscoveryUnavailable, res.Discovery)
	require.NotNil(t, res.Packs)
	require.Empty(t, res.Packs, "问不出来时不得拿推荐包冒充「这台机器上可选的东西」")
}

// TestSkillsHandler_Catalog_MissingCLIIsUnavailable CLI 根本没装在这台机器上时同样是
// 「答不出」,不是「没有包」。
func TestSkillsHandler_Catalog_MissingCLIIsUnavailable(t *testing.T) {
	restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, &fakeSkillDisc{})
	t.Cleanup(restore)
	SetResolveCLIPathFunc(func(string) (string, bool, error) { return "", false, nil })
	t.Cleanup(ResetResolveCLIPathFunc)

	h := NewSkillsHandlers()
	res, err := h.Catalog(context.Background(), wire.SkillCatalogParams{BackendType: "claudecode"})
	require.NoError(t, err)
	require.Equal(t, wire.SkillDiscoveryUnavailable, res.Discovery)
	require.Empty(t, res.Packs)
}

// TestSkillsHandler_Catalog_NoDiscovererIsUnsupported builtin / piagent 这类 backend
// 没有技能这一说。它与「答不出」必须分开:再问一次结果也一样,界面该说「不支持」
// 而不是「稍后重试」。
func TestSkillsHandler_Catalog_NoDiscovererIsUnsupported(t *testing.T) {
	h := NewSkillsHandlers()
	res, err := h.Catalog(context.Background(), wire.SkillCatalogParams{BackendType: "nonesuch"})
	require.NoError(t, err)
	require.Equal(t, wire.SkillDiscoveryUnsupported, res.Discovery)
	require.NotNil(t, res.Packs)
	require.Empty(t, res.Packs)
}
