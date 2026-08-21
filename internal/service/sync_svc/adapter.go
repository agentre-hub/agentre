package sync_svc

import (
	"context"
	"errors"

	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
	"github.com/agentre-ai/agentre/internal/repository/remote_device_repo"
	"github.com/agentre-ai/agentre/internal/repository/syncstate_repo"
)

// errRefMissing 表示引用的目标在本机还没有落地（R2a）：该行暂缓落地，不写悬空引用。
var errRefMissing = errors.New("sync: referenced object has not arrived yet")

// ref 是一份载荷依赖的跨机引用（R2）。二选一：
//   - Kind + SyncID：同步组里的另一个对象，解析成接收端的本地自增 ID；
//   - Fingerprint：一台 agentred，解析成本机 paired_agentreds 里同指纹那行的本地 ID
//     （R2b：本机没有配对它时解析不出）。
type ref struct {
	Kind        string
	SyncID      string
	Fingerprint string
}

func (r ref) key() string {
	if r.Fingerprint != "" {
		return "agentred:" + r.Fingerprint
	}
	return r.Kind + ":" + r.SyncID
}

func (r ref) empty() bool { return r.SyncID == "" && r.Fingerprint == "" }

// adapter 把一种账号级对象的「本地行 ↔ 同步载荷」收进一个窄口子。加一张新的同步表
// 就是加一个实现，引擎本身不改（OCP）。
type adapter interface {
	kind() string
	// load 按同步标识把本机那一行翻成上行内容；行已经不在了返回 (nil, nil)。
	load(ctx context.Context, syncID string) (*outbound, error)
	// refs 报出一份下行载荷依赖的跨机引用。
	refs(in *inbound) []ref
	// apply 把一份下行载荷落成本机的行（新建或更新）；resolved 按 ref.key() 给出
	// 每个引用解析出的本地 ID。本机独有的列（projects.path、头像正文、device_id
	// 缓存）一律保留不动。
	apply(ctx context.Context, in *inbound, resolved map[string]int64) error
	// remove 落地一次删除（墓碑到达，R6）。
	remove(ctx context.Context, in *inbound) error
	// dependents 列出随本行一并上行的从属行：Agent 的执行目标是独立的同步对象，
	// 但它们只跟着 Agent 的写入路径变化。
	dependents(ctx context.Context, syncID string) ([]relatedRow, error)
	// children 列出本行被删除时一并落墓碑的子行（R6）。
	children(ctx context.Context, syncID string) ([]relatedRow, error)
}

// baseAdapter 给不需要从属行 / 子行的对象类型兜底。
type baseAdapter struct{}

func (baseAdapter) dependents(context.Context, string) ([]relatedRow, error) { return nil, nil }
func (baseAdapter) children(context.Context, string) ([]relatedRow, error)   { return nil, nil }

// defaultAdapters 装配七张账号级表的适配器。avatar 是头像内容存取的窄接口，只有
// agentAdapter 用到（R16a）；单机模式下 New(nil) 传进来的是真正的 nil 接口，
// agentAdapter 据此优雅退化成只带哈希、不取正文。
func defaultAdapters(avatar avatarTransport) map[string]adapter {
	list := []adapter{
		&projectAdapter{},
		&departmentAdapter{},
		&agentAdapter{avatar: avatar},
		&agentBackendAdapter{},
		&agentBackendCLIAdapter{},
		&agentExecTargetAdapter{},
		&projectAgentAdapter{},
		&projectLocationAdapter{},
		&llmProviderAdapter{},
	}
	out := make(map[string]adapter, len(list))
	for _, a := range list {
		out[a.kind()] = a
	}
	return out
}

// ── 引用解析的共用件 ────────────────────────────────────────────────────────

// resolveRefs 解析一份载荷的全部引用；任何一个解析不出就报 errRefMissing，
// 由调用方按 R2a 暂缓落地。
func resolveRefs(ctx context.Context, refs []ref) (map[string]int64, ref, error) {
	out := make(map[string]int64, len(refs))
	for _, r := range refs {
		if r.empty() {
			continue
		}
		var (
			id  int64
			err error
		)
		if r.Fingerprint != "" {
			id, err = localIDOfFingerprint(ctx, r.Fingerprint)
		} else {
			id, err = syncstate_repo.SyncState().FindLocalID(ctx, r.Kind, r.SyncID)
		}
		if err != nil {
			return nil, ref{}, err
		}
		if id == 0 {
			return nil, r, errRefMissing
		}
		out[r.key()] = id
	}
	return out, ref{}, nil
}

// localIDOfFingerprint 把 agentred 指纹解析成本机 paired_agentreds 的行 ID。
// 判据是「本地配对表里有没有这个指纹」，不是有没有配对令牌（R2b）。
func localIDOfFingerprint(ctx context.Context, fingerprint string) (int64, error) {
	rows, err := remote_device_repo.PairedAgentred().List(ctx)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if row != nil && row.DaemonFingerprint == fingerprint {
			return row.ID, nil
		}
	}
	return 0, nil
}

// syncIDOf 取某个对象的同步标识，供上行时把本地引用翻成跨机引用。
func syncIDOf(meta syncmeta_entity.SyncMeta) string { return meta.SyncID }

// resolvedID 按 ref 取解析结果；没有这个引用时返回 0（可选引用，如顶级项目的父项目）。
func resolvedID(resolved map[string]int64, r ref) int64 {
	if r.empty() {
		return 0
	}
	return resolved[r.key()]
}

// syncKinds 是同步组的七个对象类型，按「被引用者在前」排列——认领（R12a）与任何
// 需要遍历全部类型的地方都按它走，父行因此先入队、先落地（R2a 的暂缓少绕一圈）。
var syncKinds = []string{
	syncwire.KindProject,
	syncwire.KindDepartment,
	syncwire.KindAgent,
	syncwire.KindAgentBackend,
	syncwire.KindAgentBackendCLI,
	syncwire.KindAgentExecTarget,
	syncwire.KindProjectAgent,
	syncwire.KindProjectLocation,
	syncwire.KindLLMProvider,
}

// kindKnown 报告某个对象类型是否属于同步组。
func kindKnown(kind string) bool {
	for _, k := range syncKinds {
		if k == kind {
			return true
		}
	}
	return false
}
