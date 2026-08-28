package ipc

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

func TestValidatePermissionMode_DefaultMode(t *testing.T) {
	Convey("空 raw 返回 backend default mode", t, func() {
		got, err := ValidatePermissionMode(context.Background(), agent_backend_entity.TypeClaudeCode, "")
		So(err, ShouldBeNil)
		So(got, ShouldEqual, "acceptEdits")
	})
}

func TestValidatePermissionMode_Allowed(t *testing.T) {
	Convey("命中 AllowedModes 时原值返回", t, func() {
		got, err := ValidatePermissionMode(context.Background(), agent_backend_entity.TypeClaudeCode, "plan")
		So(err, ShouldBeNil)
		So(got, ShouldEqual, "plan")
	})
}

func TestValidatePermissionMode_Invalid(t *testing.T) {
	Convey("不命中时报错", t, func() {
		_, err := ValidatePermissionMode(context.Background(), agent_backend_entity.TypeClaudeCode, "nonsense")
		So(err, ShouldNotBeNil)
	})
}

func TestValidatePermissionMode_UnknownBackendNoCaps(t *testing.T) {
	Convey("未知 backend caps 为空时,raw 非空一律报错", t, func() {
		_, err := ValidatePermissionMode(context.Background(), "unknown", "default")
		So(err, ShouldNotBeNil)
	})
}
