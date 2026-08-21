package sync_svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
	"github.com/agentre-ai/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-ai/agentre/internal/repository/project_repo"
)

// localPathReportSettingKey 是「上次成功上报的内容指纹」的存放位置——与
// cursorSettingKey 同一模式，住在本地 key-value 设置表，记账号是同样的理由
// （R13a：换账号后这份状态不该沿用）。
const localPathReportSettingKey = "sync.local_paths.report"

// localPathReportState 是本机路径上报侧的进度：上次成功上报的整份快照内容指纹，
// 连同它属于哪个账号。
type localPathReportState struct {
	AccountID   int64  `json:"account_id"`
	Fingerprint string `json:"fingerprint"`
	ReportedAt  int64  `json:"reported_at"`
}

func (s *service) loadLocalPathReportState(ctx context.Context, accountID int64) (localPathReportState, error) {
	row, err := app_setting_repo.AppSetting().Get(ctx, localPathReportSettingKey)
	if err != nil {
		return localPathReportState{AccountID: accountID}, err
	}
	if row == nil || row.Value == "" {
		return localPathReportState{AccountID: accountID}, nil
	}
	var st localPathReportState
	if err := json.Unmarshal([]byte(row.Value), &st); err != nil {
		// 存坏了的状态当成「从没成功上报过」：下一轮内容指纹必然不同，照常重发。
		return localPathReportState{AccountID: accountID}, nil //nolint:nilerr // 见上
	}
	if st.AccountID != accountID {
		return localPathReportState{AccountID: accountID}, nil
	}
	return st, nil
}

func (s *service) saveLocalPathReportState(ctx context.Context, st localPathReportState) error {
	body, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
		Key:        localPathReportSettingKey,
		Value:      string(body),
		Updatetime: s.now(),
	})
}

// buildLocalPathSnapshot 读一份「本机为哪些项目配了路径」的整份快照（R16）。纯
// 读取——project_repo.List 是这个函数唯一碰到的仓储调用，不写入任何本地表（R17）。
//
// accountID 是当前登录账号：属于**另一个**账号的行一律不进快照（R13a）。这条与
// 账号级对象上行的门（notify.go 的 EligibleForSync）是同一条边界，且在这里分量更
// 重——快照带的是那台机器上的绝对路径，隐私一节写明「一台共用机器上换账号登录，
// 不应把上一个账号的项目名、Agent 与路径发到新账号下」。
func (s *service) buildLocalPathSnapshot(ctx context.Context, accountID int64) ([]syncwire.LocalPathReportItem, error) {
	rows, err := project_repo.Project().List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]syncwire.LocalPathReportItem, 0, len(rows))
	for _, p := range rows {
		if p == nil || p.LocalPathMissing || p.SyncID == "" {
			continue
		}
		if !p.EligibleForSync(accountID) {
			continue
		}
		path := strings.TrimSpace(p.Path)
		if path == "" {
			continue
		}
		items = append(items, syncwire.LocalPathReportItem{ProjectSyncID: p.SyncID, Path: p.Path})
	}
	// 排序让指纹只取决于内容、不取决于 project_repo.List 的行序——否则同一份快照
	// 可能因为无关的排序波动被判成「变了」，白发一次网络请求。
	sort.Slice(items, func(i, j int) bool { return items[i].ProjectSyncID < items[j].ProjectSyncID })
	return items, nil
}

// localPathSnapshotFingerprint 是整份快照的内容指纹（R16）：本机路径的修改不触及
// 任何一张表的 Updatetime 之外的痕迹，判「发不发」只能靠对内容取哈希，不能靠时间戳。
func localPathSnapshotFingerprint(items []syncwire.LocalPathReportItem) string {
	h := sha256.New()
	for _, it := range items {
		h.Write([]byte(it.ProjectSyncID))
		h.Write([]byte{0})
		h.Write([]byte(it.Path))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ReportLocalPathsNow 见 SyncSvc 接口注释（规格 2026-08-21 决策 4）。
//
// 它就是 reportLocalPathsOnce，只是从轮询之外多开了一个调用点：内容指纹、跳过
// 逻辑、失败时不推进指纹这三条一律照旧。**刻意不是另写一份「强制上报」**——
// 同一件事两份实现迟早给出两个答案，而这里的答案是「服务端那份清单等不等于本地」。
func (s *service) ReportLocalPathsNow(ctx context.Context) error {
	return s.reportLocalPathsOnce(ctx)
}

// reportLocalPathsOnce 是本机路径上报的唯一入口（R16）：只被 Start 的 30 秒轮询
// ticker 调用，NotifyLocalChange 触发的编辑当场上行绝不途经它——上报与「编辑当场
// 上行」刻意解耦，见 Start 的注释。
//
// 只有这一轮算出的内容指纹与上次成功上报的不同时才真的发一次网络请求；相同则
// 跳过。上报失败时不推进已保存的指纹：下一轮（30 秒后）内容指纹依旧不同，自然
// 重试，服务端保留的是它上一次成功收到的那份清单，不受影响。
func (s *service) reportLocalPathsOnce(ctx context.Context) error {
	accountID, _, ok := s.account(ctx)
	transport := s.getTransport()
	if !ok || transport == nil {
		return nil
	}

	items, err := s.buildLocalPathSnapshot(ctx, accountID)
	if err != nil {
		return err
	}
	fingerprint := localPathSnapshotFingerprint(items)

	st, err := s.loadLocalPathReportState(ctx, accountID)
	if err != nil {
		return err
	}
	if st.Fingerprint == fingerprint {
		return nil
	}

	// 路径本身绝不进日志（决策：路径含个人信息，不得写日志）——这里只报条数。
	if err := transport.ReportLocalPaths(ctx, items); err != nil {
		logger.Ctx(ctx).Debug("sync_svc.reportLocalPathsOnce: report failed, keeping previous snapshot",
			zap.Int("itemCount", len(items)), zap.Error(err))
		return err
	}
	return s.saveLocalPathReportState(ctx, localPathReportState{
		AccountID: accountID, Fingerprint: fingerprint, ReportedAt: s.now(),
	})
}
