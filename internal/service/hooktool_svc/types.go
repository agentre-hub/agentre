package hooktool_svc

import "github.com/agentre-hub/agentre/internal/service/hook_svc"

// 写工具参数 struct。create 用值类型(全量提供);update 用指针区分"不传(沿用现值)"
// 与"显式置空";env 用 *[]EnvVar 区分"不动 env"与"整体替换"。

type createHookArgs struct {
	Name            string            `json:"name"`
	Interpreter     string            `json:"interpreter"`
	InterpreterPath string            `json:"interpreterPath"`
	Command         string            `json:"command"`
	ScheduleExpr    string            `json:"scheduleExpr"`
	Timezone        string            `json:"timezone"`
	Env             []hook_svc.EnvVar `json:"env"`
	Enabled         *bool             `json:"enabled"` // 省略=默认启用
}

type updateHookArgs struct {
	ID              int64              `json:"id"`
	Name            *string            `json:"name"`
	Interpreter     *string            `json:"interpreter"`
	InterpreterPath *string            `json:"interpreterPath"`
	Command         *string            `json:"command"`
	ScheduleExpr    *string            `json:"scheduleExpr"`
	Timezone        *string            `json:"timezone"`
	Env             *[]hook_svc.EnvVar `json:"env"` // 非 nil=整体替换;nil=沿用现值(密钥 ******** 由 hook_svc 保留)
	Enabled         *bool              `json:"enabled"`
}

type deleteHookArgs struct {
	ID int64 `json:"id"`
}

type runHookArgs struct {
	ID     int64 `json:"id"`
	DryRun *bool `json:"dryRun"` // 省略=true(默认试运行,不落库)
}

type getHookArgs struct {
	ID int64 `json:"id"`
}
