package sync_svc

import (
	"context"
	"encoding/json"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
)

// cursorSettingKey 是下行游标的存放位置。它是本机的同步进度，不是账号级对象，
// 因此住在本地 key-value 设置表里而不是同步组的任何一张表上。
const cursorSettingKey = "sync.cursor"

// cursorState 是游标本身连同它属于哪个账号。
//
// 记账号是 R13a 的要求：换账号登录后，上一个账号的游标一律作废——沿用它会让新账号
// 从别人的版本号开始拉，整份历史都拉不到。
type cursorState struct {
	AccountID     int64 `json:"account_id"`
	Cursor        int64 `json:"cursor"`
	LastSuccessAt int64 `json:"last_success_at"`
}

func (s *service) loadCursor(ctx context.Context, accountID int64) (cursorState, error) {
	row, err := app_setting_repo.AppSetting().Get(ctx, cursorSettingKey)
	if err != nil {
		return cursorState{AccountID: accountID}, err
	}
	if row == nil || row.Value == "" {
		return cursorState{AccountID: accountID}, nil
	}
	var st cursorState
	if err := json.Unmarshal([]byte(row.Value), &st); err != nil {
		// 存坏了的游标当成「没有游标」：下一轮从 0 拉一份全量，比让同步停摆好。
		return cursorState{AccountID: accountID}, nil //nolint:nilerr // 见上
	}
	if st.AccountID != accountID {
		return cursorState{AccountID: accountID}, nil
	}
	return st, nil
}

func (s *service) saveCursor(ctx context.Context, st cursorState) error {
	body, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
		Key:        cursorSettingKey,
		Value:      string(body),
		Updatetime: s.now(),
	})
}
