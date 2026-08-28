package server_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
)

// 本文件是工作区多端同步的网络出入口：只做 HTTP 与编解码，一切判定（冲突、墓碑、
// 暂缓、队列）在 sync_svc 里。它不 import sync_svc——线上结构住在叶子包 syncwire，
// 两边各自依赖它。
//
// 载荷内容一律不进日志：里面有项目路径、prompt 与 EnvJSON。

type syncPushReqItem struct {
	Kind        string `json:"kind"`
	SyncID      string `json:"sync_id"`
	BaseVersion int64  `json:"base_version"`
	UpdatedAt   int64  `json:"updated_at"`
	// DeletedAt 非零 = 这是一条墓碑，值是本端记下的删除时刻（Unix 毫秒）。契约上是
	// **时刻**而不是布尔：时刻在本端库、线格式与 server 库三处本来就都是时刻，压成
	// 布尔之后 server 落地只能另行编造一个删除时间（规格
	// 2026-08-27-schema-overhaul 决策 20）。
	DeletedAt           int64  `json:"deleted_at"`
	AgentredFingerprint string `json:"agentred_fingerprint"`
	ProjectSyncID       string `json:"project_sync_id"`
	// Payload 必须 omitempty：墓碑不带正文（buildPushItem 的 delete 分支），而
	// json.RawMessage 的零值编码出来是 JSON null——null 不是对象，server 的
	// ValidatePayload 会整批拒（30501），一次删除就把出站队列永久堵死（R6/R7）。
	Payload json.RawMessage `json:"payload,omitempty"`
}

type syncPushReq struct {
	Items []syncPushReqItem `json:"items"`
}

type syncPushResp struct {
	Results []syncwire.PushResult `json:"results"`
}

type syncPullRespItem struct {
	Kind                string          `json:"kind"`
	SyncID              string          `json:"sync_id"`
	ProjectSyncID       string          `json:"project_sync_id"`
	AgentredFingerprint string          `json:"agentred_fingerprint"`
	Payload             json.RawMessage `json:"payload"`
	Version             int64           `json:"version"`
	UpdatedAt           int64           `json:"updated_at"`
	// OriginFingerprint 是最后一次修改来自哪台机器（决策 14：跨机引用一律用指纹，
	// server 的数值设备主键是它的本地键，本端离线创建的行没有它）。空串 = server 直写。
	OriginFingerprint string `json:"origin_fingerprint"`
	// DeletedAt 非零 = 墓碑，值是删除时刻（决策 20）。
	DeletedAt int64 `json:"deleted_at"`
}

type syncPullResp struct {
	Items      []syncPullRespItem `json:"items"`
	NextCursor int64              `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
}

// SyncPush 把一批本地改动上行。未登录时不发任何网络请求（R12）。
//
// server 判「设备距上次成功同步已超过墓碑保留窗口」时返回 CodeResyncRequired，
// 这里翻成 syncwire.ErrResyncRequired——调用方据此先拉一份全量快照（R6a）。
func (s *service) SyncPush(ctx context.Context, items []syncwire.PushItem) ([]syncwire.PushResult, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if err := s.requireLogin(ctx); err != nil {
		return nil, err
	}

	req := syncPushReq{Items: make([]syncPushReqItem, 0, len(items))}
	for _, it := range items {
		req.Items = append(req.Items, syncPushReqItem{
			Kind:                it.Kind,
			SyncID:              it.SyncID,
			BaseVersion:         it.BaseVersion,
			UpdatedAt:           it.UpdatedAt,
			DeletedAt:           it.DeletedAt,
			AgentredFingerprint: it.AgentredFingerprint,
			ProjectSyncID:       it.ProjectSyncID,
			Payload:             json.RawMessage(it.Payload),
		})
	}

	var out []syncwire.PushResult
	err := s.withAuth(ctx, func(ctx context.Context) error {
		var env envelope[syncPushResp]
		_, doErr := s.getClient().do(ctx, http.MethodPost, "/v1/sync/push", req, &env)
		if env.Code == syncwire.CodeResyncRequired {
			return syncwire.ErrResyncRequired
		}
		if doErr != nil {
			return doErr
		}
		if env.Code != 0 {
			return fmt.Errorf("server: sync push rejected with code %d", env.Code)
		}
		out = env.Data.Results
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SyncPull 按版本游标增量下行；cursor = 0 拉全量（R6a 的重同步用它）。
//
// server 判「这个游标超出本账号版本序列的头」时返回 CodeCursorUnknown，这里翻成
// syncwire.ErrCursorUnknown——调用方据此重建整份历史并把 server 不认识的本地行重新
// 上行。不翻的话它只是一句「rejected with code 30505」，与网络抖动无从区分，那台机器
// 会安静地一直重试同一个死游标。
func (s *service) SyncPull(ctx context.Context, cursor int64, limit int) (*syncwire.PullPage, error) {
	if err := s.requireLogin(ctx); err != nil {
		return nil, err
	}

	path := "/v1/sync/pull?cursor=" + strconv.FormatInt(cursor, 10)
	if limit > 0 {
		path += "&limit=" + strconv.Itoa(limit)
	}

	page := &syncwire.PullPage{}
	err := s.withAuth(ctx, func(ctx context.Context) error {
		var env envelope[syncPullResp]
		_, doErr := s.getClient().do(ctx, http.MethodGet, path, nil, &env)
		if env.Code == syncwire.CodeCursorUnknown {
			return syncwire.ErrCursorUnknown
		}
		if doErr != nil {
			return doErr
		}
		if env.Code != 0 {
			return fmt.Errorf("server: sync pull rejected with code %d", env.Code)
		}
		items := make([]syncwire.PullItem, 0, len(env.Data.Items))
		for _, it := range env.Data.Items {
			items = append(items, syncwire.PullItem{
				Kind:                it.Kind,
				SyncID:              it.SyncID,
				ProjectSyncID:       it.ProjectSyncID,
				AgentredFingerprint: it.AgentredFingerprint,
				Payload:             []byte(it.Payload),
				Version:             it.Version,
				UpdatedAt:           it.UpdatedAt,
				OriginFingerprint:   it.OriginFingerprint,
				DeletedAt:           it.DeletedAt,
			})
		}
		page.Items = items
		page.NextCursor = env.Data.NextCursor
		page.HasMore = env.Data.HasMore
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

// ---------- 上报组：本机路径 ----------

type localPathReqItem struct {
	ProjectSyncID string `json:"project_sync_id"`
	Path          string `json:"path"`
}

type reportLocalPathsReq struct {
	Items []localPathReqItem `json:"items"`
}

// ReportLocalPaths 把本机路径整份快照上报给 server（R16）；未登录不发任何请求
// （R12）。路径本身不进日志，出错时只把 server 的错误原样透出，调用方据此重试。
func (s *service) ReportLocalPaths(ctx context.Context, items []syncwire.LocalPathReportItem) error {
	if err := s.requireLogin(ctx); err != nil {
		return err
	}
	req := reportLocalPathsReq{Items: make([]localPathReqItem, 0, len(items))}
	for _, it := range items {
		req.Items = append(req.Items, localPathReqItem{ProjectSyncID: it.ProjectSyncID, Path: it.Path})
	}
	return s.withAuth(ctx, func(ctx context.Context) error {
		var env envelope[struct{}]
		_, doErr := s.getClient().do(ctx, http.MethodPost, "/v1/sync/local-paths", req, &env)
		if doErr != nil {
			return doErr
		}
		if env.Code != 0 {
			return fmt.Errorf("server: report local paths rejected with code %d", env.Code)
		}
		return nil
	})
}

// ---------- 头像 ----------

type putAvatarReq struct {
	ContentHash string `json:"content_hash"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
}

// PutAvatar 把本机持有的头像正文按内容哈希推给对端（R16a）；server 端按哈希幂等
// 落库，重复上传同一份内容不产生额外开销。
func (s *service) PutAvatar(ctx context.Context, contentHash, contentType, content string) error {
	if err := s.requireLogin(ctx); err != nil {
		return err
	}
	return s.withAuth(ctx, func(ctx context.Context) error {
		var env envelope[struct{}]
		_, doErr := s.getClient().do(ctx, http.MethodPost, "/v1/sync/avatars",
			putAvatarReq{ContentHash: contentHash, ContentType: contentType, Content: content}, &env)
		if doErr != nil {
			return doErr
		}
		if env.Code != 0 {
			return fmt.Errorf("server: put avatar rejected with code %d", env.Code)
		}
		return nil
	})
}

type getAvatarResp struct {
	ContentHash string `json:"content_hash"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
}

// GetAvatar 取一份尚未持有的头像正文（R16a）；取不到时把 server 的错误原样透出，
// 调用方（agentAdapter）据此降级为占位字母头像，不重试到无限。
func (s *service) GetAvatar(ctx context.Context, contentHash string) (string, string, error) {
	if err := s.requireLogin(ctx); err != nil {
		return "", "", err
	}
	var content, contentType string
	err := s.withAuth(ctx, func(ctx context.Context) error {
		var env envelope[getAvatarResp]
		path := "/v1/sync/avatars?content_hash=" + url.QueryEscape(contentHash)
		_, doErr := s.getClient().do(ctx, http.MethodGet, path, nil, &env)
		if doErr != nil {
			return doErr
		}
		if env.Code != 0 {
			return fmt.Errorf("server: get avatar rejected with code %d", env.Code)
		}
		content = env.Data.Content
		contentType = env.Data.ContentType
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return content, contentType, nil
}

// requireLogin 未登录时挡在网络请求之前（R12：未登录不产生任何同步网络请求）。
func (s *service) requireLogin(ctx context.Context) error {
	row, err := server_state_repo.ServerState().Get(ctx)
	if err != nil {
		return err
	}
	if row == nil || !row.IsLoggedIn() {
		return ErrNotLoggedIn
	}
	return nil
}
