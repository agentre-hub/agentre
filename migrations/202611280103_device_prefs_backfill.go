package migrations

import (
	"strings"
	"time"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

// migration202611280103 回填设备偏好（spec 2026-11-25「迁移」规则 4、5）。
//
// 规则 4 设备顺序：本机第一，其余已配对设备按配对时间。没有指纹的桌面端（从未配对、从未
// 登录）没有本机这一行，顺序里只有已配对的设备。本端按 Agent 存的执行目标顺序覆盖表
// （agent_exec_target_overrides）不读也不删，留给后续任务一起移除；控制台的 sort_order 不在
// 本机库里。
//
// 规则 5 CLI 路径覆盖：按（运行时类型, 设备）取该类型各后端里第一条非空覆盖。「第一」：活后端
// 在前、已删除的后端垫后（用户填过的路径不因后端删了而丢，真数据里的覆盖就挂在已删后端上），
// 同档按后端 sync_id 字典序（与引擎配置合并同一口径，各端得到同样的结果），同一后端按覆盖行 id。
//
// 影响的数据：只新增 device_prefs / device_cli_paths 的行，不改任何旧表旧行；没有不可逆的部分。
func migration202611280103() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202611280103",
		Migrate: func(tx *gorm.DB) error {
			return tx.Transaction(func(tx *gorm.DB) error {
				if err := backfillDeviceOrder(tx, keychain.Default()); err != nil {
					return err
				}
				return backfillCLIPaths(tx)
			})
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec("DELETE FROM device_cli_paths").Error; err != nil {
				return err
			}
			return tx.Exec("DELETE FROM device_prefs").Error
		},
	}
}

func backfillDeviceOrder(tx *gorm.DB, kc keychain.Keychain) error {
	order := make([]string, 0, 8)
	seen := map[string]struct{}{}
	add := func(fp string) {
		fp = strings.TrimSpace(fp)
		if fp == "" {
			return
		}
		if _, dup := seen[fp]; dup {
			return
		}
		seen[fp] = struct{}{}
		order = append(order, fp)
	}
	add(selfDeviceFingerprint(kc))
	var paired []string
	if err := tx.Raw(`SELECT daemon_fingerprint FROM paired_agentreds WHERE status = 1 ORDER BY paired_at ASC, id ASC`).Scan(&paired).Error; err != nil {
		return err
	}
	for _, fp := range paired {
		add(fp)
	}
	now := time.Now().UnixMilli()
	for i, fp := range order {
		if err := tx.Exec(`INSERT INTO device_prefs (device_fingerprint, labels_json, sort_order, status, createtime, updatetime, sync_id)
SELECT ?, '[]', ?, 1, ?, ?, ?
WHERE NOT EXISTS (SELECT 1 FROM device_prefs WHERE device_fingerprint = ? AND status = 1)`,
			fp, i, now, now, syncmeta_entity.NewSyncID(), fp).Error; err != nil {
			return err
		}
	}
	return nil
}

type migOverlay struct {
	Type        string `gorm:"column:type"`
	Fingerprint string `gorm:"column:agentred_fingerprint"`
	CLIPath     string `gorm:"column:cli_path"`
}

func backfillCLIPaths(tx *gorm.DB) error {
	var rows []migOverlay
	if err := tx.Raw(`SELECT b.type AS type, o.agentred_fingerprint AS agentred_fingerprint, o.cli_path AS cli_path
FROM agent_backend_cli_overlays o
JOIN agent_backends b ON b.sync_id = o.backend_sync_id
WHERE o.status = 1 AND b.sync_id != ''
  AND TRIM(o.cli_path) != '' AND TRIM(o.agentred_fingerprint) != ''
ORDER BY (b.status != 1) ASC, b.sync_id ASC, o.id ASC`).Scan(&rows).Error; err != nil {
		return err
	}
	type key struct{ runtime, fp string }
	done := map[key]struct{}{}
	now := time.Now().UnixMilli()
	for _, r := range rows {
		k := key{r.Type, strings.TrimSpace(r.Fingerprint)}
		if _, dup := done[k]; dup {
			continue
		}
		done[k] = struct{}{}
		if err := tx.Exec(`INSERT INTO device_cli_paths (runtime_type, device_fingerprint, cli_path, status, createtime, updatetime, sync_id)
VALUES (?, ?, ?, 1, ?, ?, ?)`,
			k.runtime, k.fp, strings.TrimSpace(r.CLIPath), now, now, syncmeta_entity.NewSyncID()).Error; err != nil {
			return err
		}
	}
	return nil
}
