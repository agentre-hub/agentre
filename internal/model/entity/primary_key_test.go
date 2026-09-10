package entity_test

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/hook_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/sync_account_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
)

// allEntities 是每一个绑定了表名的实体。新增实体时在这里登记 —— 表侧的同一条约定由
// migrations 包的 TestEveryTableHasAutoIncrementIDPrimaryKey 独立钉住。
func allEntities() []any {
	return []any{
		&agent_backend_entity.AgentBackend{},
		&agent_backend_entity.CLIOverlay{},
		&agent_entity.Agent{},
		&agent_entity.AgentExecTarget{},
		&agent_entity.AgentExecTargetOverride{},
		&app_setting_entity.AppSetting{},
		&chat_entity.Session{},
		&department_entity.Department{},
		&hook_entity.Hook{},
		&hook_entity.HookEvent{},
		&issue_entity.Issue{},
		&issue_entity.IssueLabel{},
		&issue_entity.Label{},
		&llm_provider_entity.LLMProvider{},
		&llm_provider_model_entity.LLMProviderModel{},
		&paired_agentred_entity.PairedAgentred{},
		&project_entity.Project{},
		&project_entity.ProjectAgent{},
		&project_location_entity.ProjectLocation{},
		&server_state_entity.ServerState{},
		&sync_account_entity.SyncAccount{},
		&syncqueue_entity.InboundQueueItem{},
		&syncqueue_entity.LostChange{},
		&syncqueue_entity.OutboundQueueItem{},
		&transcript_entity.Message{},
		&transcript_entity.MessageBlock{},
	}
}

// TestEveryEntityHasAutoIncrementIDPrimaryKey 断言每个实体都以单列自增 ID 为主键。
//
// 实体侧与表侧必须说同一件事：GORM 从主键推 WHERE（Save / First / Delete(&e{})），
// 实体上的主键声明与 DDL 差一格，同一段代码就会在内存里和库里各认一套身份。
func TestEveryEntityHasAutoIncrementIDPrimaryKey(t *testing.T) {
	cache := &sync.Map{}
	for _, model := range allEntities() {
		sch, err := schema.Parse(model, cache, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("parse %T: %v", model, err)
		}
		t.Run(sch.Table, func(t *testing.T) {
			if len(sch.PrimaryFields) != 1 {
				names := make([]string, 0, len(sch.PrimaryFields))
				for _, f := range sch.PrimaryFields {
					names = append(names, f.DBName)
				}
				t.Fatalf("primary key = %v, want [id]", names)
			}
			pk := sch.PrimaryFields[0]
			if pk.DBName != "id" {
				t.Errorf("primary key column = %q, want \"id\"", pk.DBName)
			}
			if !pk.AutoIncrement {
				t.Errorf("primary key %q is not autoIncrement", pk.DBName)
			}
		})
	}
}
