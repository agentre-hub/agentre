package migrations

import (
	"fmt"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
)

// issueSyncTables 是本轮并入账号级同步组的三张表。顺序按「被引用者在前」，与同步
// 适配器里的 kind 顺序同源。
var issueSyncTables = []string{"labels", "issues", "issue_labels"}

// issueSyncColumns 与 internal/model/entity/syncmeta_entity.SyncMeta 的字段一一对应，
// 形状与 202608080012 给账号级七张表加的那六列相同（那一份记的是 202608270003 改名
// **之前**的列名，所以这里重列一份而不是复用它——迁移一旦跑过就不能再随别处漂移）。
var issueSyncColumns = []struct{ name, ddl string }{
	{"sync_id", "TEXT NOT NULL DEFAULT ''"},
	{"sync_account_id", "BIGINT NOT NULL DEFAULT 0"},
	{"sync_version", "BIGINT NOT NULL DEFAULT 0"},
	{"sync_updated_at", "BIGINT NOT NULL DEFAULT 0"},
	{"sync_origin_fingerprint", "TEXT NOT NULL DEFAULT ''"},
	{"sync_deleted_at", "BIGINT NOT NULL DEFAULT 0"},
}

// issueToneRewrite 是五档语义名 → 8 档颜色名的 1:1 映射（决策 6）。标签的 name **不动**
// —— 改的是色调取值域，不是标签本身。
var issueToneRewrite = [][2]string{
	{"bug", issue_entity.ToneRed},
	{"critical", issue_entity.ToneRedSolid},
	{"docs", issue_entity.ToneGray},
	{"feature", issue_entity.ToneGreen},
	{"refactor", issue_entity.ToneSteel},
}

// migration202608280002 把看板并入账号级同步组，并落下执行归属与新的色调取值域：
//
//  1. issues 增加 agent_backend_id / llm_provider_key / llm_model_key 三列（复用既有的
//     assignee_agent_id）。本轮没有任何路径读它们——存下来是为了让任务带着「打算怎么
//     跑」，真正的派发是后续的事（决策 9）。
//  2. issues / labels / issue_labels 三张表各加六列同步元数据 + sync_id 部分唯一索引，
//     形状与 202608080012 给账号级七张表加的那六列逐字相同。
//  3. 逐行补发 sync_id。与 202608080012 刻意留空、等仓储层 JIT 补齐不同，这三张表的
//     既有行在这里就地补齐：任务与标签一并入组即刻可上行，不必等到被编辑一次。补发
//     **只写空值列**，不改任何既有字段。
//     内置的五个种子标签在每台机器上都存在同一份（都来自 202608080010 的同一条 seed）。
//     照常随机取值的话，同一个「前端」标签首次上行后会在账号里变成 N 份；因此它们的
//     标识按名字确定性派生（issue_entity.SeedLabelSyncID），两台机器天然收敛成同一个
//     对象。用户自建的标签照常随机取标识。
//  4. labels.tone 的取值域从五个语义名改为 8 个颜色名，既有行按 1:1 映射就地改写。
//
// issues.agent_status 与 issues.source 本轮**不删除**（删列属于另一件事），只是界面
// 不再读取它们。
func migration202608280002() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608280002",
		Migrate: func(tx *gorm.DB) error {
			for _, col := range []struct{ name, ddl string }{
				{"agent_backend_id", "BIGINT NOT NULL DEFAULT 0"},
				{"llm_provider_key", "TEXT NOT NULL DEFAULT ''"},
				{"llm_model_key", "TEXT NOT NULL DEFAULT ''"},
			} {
				if err := tx.Exec(fmt.Sprintf(
					`ALTER TABLE issues ADD COLUMN %s %s`, col.name, col.ddl)).Error; err != nil {
					return fmt.Errorf("issues: add column %s: %w", col.name, err)
				}
			}

			for _, table := range issueSyncTables {
				for _, col := range issueSyncColumns {
					if err := tx.Exec(fmt.Sprintf(
						`ALTER TABLE %s ADD COLUMN %s %s`, table, col.name, col.ddl)).Error; err != nil {
						return fmt.Errorf("table %s: add column %s: %w", table, col.name, err)
					}
				}
				if err := tx.Exec(fmt.Sprintf(
					`CREATE UNIQUE INDEX IF NOT EXISTS uniq_%s_sync_id ON %s(sync_id) WHERE sync_id != ''`,
					table, table)).Error; err != nil {
					return fmt.Errorf("table %s: create sync_id index: %w", table, err)
				}
			}

			// 种子标签要在 tone 被改写**之前**认出来：seed 出来的行 name 就等于 tone，
			// 改写之后这个判据就没了。
			var seeds []struct {
				ID   int64  `gorm:"column:id"`
				Name string `gorm:"column:name"`
			}
			if err := tx.Raw(
				`SELECT id, name FROM labels WHERE name = tone AND name IN ?`,
				issue_entity.BuiltinLabelNames(),
			).Scan(&seeds).Error; err != nil {
				return fmt.Errorf("labels: read seed rows: %w", err)
			}

			for _, rewrite := range issueToneRewrite {
				if err := tx.Exec(
					`UPDATE labels SET tone = ? WHERE tone = ?`, rewrite[1], rewrite[0]).Error; err != nil {
					return fmt.Errorf("labels: rewrite tone %s: %w", rewrite[0], err)
				}
			}

			for _, seed := range seeds {
				if err := tx.Exec(`UPDATE labels SET sync_id = ? WHERE id = ? AND sync_id = ''`,
					issue_entity.SeedLabelSyncID(seed.Name), seed.ID).Error; err != nil {
					return fmt.Errorf("labels: backfill seed sync_id %s: %w", seed.Name, err)
				}
			}

			if err := backfillSyncIDs(tx, "labels", "id"); err != nil {
				return err
			}
			if err := backfillSyncIDs(tx, "issues", "id"); err != nil {
				return err
			}
			return backfillIssueLabelSyncIDs(tx)
		},
		Rollback: func(tx *gorm.DB) error {
			for _, table := range issueSyncTables {
				if err := tx.Exec(fmt.Sprintf(`DROP INDEX IF EXISTS uniq_%s_sync_id`, table)).Error; err != nil {
					return err
				}
				for _, col := range issueSyncColumns {
					if err := tx.Exec(fmt.Sprintf(
						`ALTER TABLE %s DROP COLUMN %s`, table, col.name)).Error; err != nil {
						return err
					}
				}
			}
			for _, col := range []string{"agent_backend_id", "llm_provider_key", "llm_model_key"} {
				if err := tx.Exec(`ALTER TABLE issues DROP COLUMN ` + col).Error; err != nil {
					return err
				}
			}
			for _, rewrite := range issueToneRewrite {
				if err := tx.Exec(
					`UPDATE labels SET tone = ? WHERE tone = ?`, rewrite[0], rewrite[1]).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}

// backfillSyncIDs 给单主键表逐行补发同步标识（只写空值列）。标识必须一行一个，
// 不能用一条 UPDATE 批量写同一个值 —— 那样整张表会共用一个标识。
func backfillSyncIDs(tx *gorm.DB, table, pk string) error {
	var ids []int64
	if err := tx.Raw(fmt.Sprintf(
		`SELECT %s FROM %s WHERE sync_id = ''`, pk, table)).Scan(&ids).Error; err != nil {
		return fmt.Errorf("table %s: read rows missing sync_id: %w", table, err)
	}
	for _, id := range ids {
		if err := tx.Exec(fmt.Sprintf(`UPDATE %s SET sync_id = ? WHERE %s = ?`, table, pk),
			syncmeta_entity.NewSyncID(), id).Error; err != nil {
			return fmt.Errorf("table %s: backfill sync_id: %w", table, err)
		}
	}
	return nil
}

// backfillIssueLabelSyncIDs 同上，只是 issue_labels 的主键是 (issue_id, label_id) 两列。
func backfillIssueLabelSyncIDs(tx *gorm.DB) error {
	var rows []struct {
		IssueID int64 `gorm:"column:issue_id"`
		LabelID int64 `gorm:"column:label_id"`
	}
	if err := tx.Raw(`SELECT issue_id, label_id FROM issue_labels WHERE sync_id = ''`).
		Scan(&rows).Error; err != nil {
		return fmt.Errorf("issue_labels: read rows missing sync_id: %w", err)
	}
	for _, row := range rows {
		if err := tx.Exec(
			`UPDATE issue_labels SET sync_id = ? WHERE issue_id = ? AND label_id = ?`,
			syncmeta_entity.NewSyncID(), row.IssueID, row.LabelID).Error; err != nil {
			return fmt.Errorf("issue_labels: backfill sync_id: %w", err)
		}
	}
	return nil
}
