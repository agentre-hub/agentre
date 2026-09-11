package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// D15：用户移除过的机器只剩按指纹的移除记录。设备面板要知道哪些指纹被移除过（隐藏它们、
// 给出「已移除」入口），恢复则删掉这台机器的移除记录，让下一轮收编把它请回来。
func TestListRemoved(t *testing.T) {
	Convey("lists each removed machine once by fingerprint, newest record first, skipping fingerprintless records", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		// ListDeleted 按 id 倒序给出，同一台机器可能被移除过不止一次。
		repo.EXPECT().ListDeleted(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 9, Name: "build-box (renamed)", DaemonFingerprint: "sha256:aaa"},
			{ID: 7, Name: "gpu-node", DaemonFingerprint: "sha256:bbb"},
			{ID: 5, Name: "old pairing", DaemonFingerprint: ""},
			{ID: 3, Name: "build-box", DaemonFingerprint: "sha256:aaa"},
		}, nil)

		removed, err := svc.ListRemoved(context.Background())
		So(err, ShouldBeNil)
		So(removed, ShouldResemble, []remote_device_svc.RemovedDevice{
			{Fingerprint: "sha256:aaa", Name: "build-box (renamed)"},
			{Fingerprint: "sha256:bbb", Name: "gpu-node"},
		})
	})

	Convey("no removal records: an empty, non-nil list", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)

		removed, err := svc.ListRemoved(context.Background())
		So(err, ShouldBeNil)
		So(removed, ShouldNotBeNil)
		So(removed, ShouldBeEmpty)
	})

	Convey("repository failure surfaces", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, errors.New("db"))

		_, err := svc.ListRemoved(context.Background())
		So(err, ShouldNotBeNil)
	})
}

func TestRestore(t *testing.T) {
	Convey("purges every removal record of that machine and leaves other machines' records alone", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 9, DaemonFingerprint: "sha256:aaa"},
			{ID: 7, DaemonFingerprint: "sha256:bbb"},
			{ID: 3, DaemonFingerprint: "sha256:aaa"},
		}, nil)
		repo.EXPECT().Purge(gomock.Any(), int64(9)).Return(nil)
		repo.EXPECT().Purge(gomock.Any(), int64(3)).Return(nil)
		// 没有 Purge(7) 的 EXPECT：另一台机器的移除记录不动。

		err := svc.Restore(context.Background(), " sha256:aaa ")
		So(err, ShouldBeNil)
	})

	Convey("a machine with no removal record: nothing to do, no error", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 7, DaemonFingerprint: "sha256:bbb"},
		}, nil)

		err := svc.Restore(context.Background(), "sha256:aaa")
		So(err, ShouldBeNil)
	})

	Convey("an empty fingerprint is rejected without touching the repository", t, func() {
		_, _, _, _, svc := setupSvc(t)

		err := svc.Restore(context.Background(), "  ")
		So(err, ShouldNotBeNil)
	})

	Convey("a purge failure surfaces", t, func() {
		repo, _, _, _, svc := setupSvc(t)
		repo.EXPECT().ListDeleted(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 9, DaemonFingerprint: "sha256:aaa"},
		}, nil)
		repo.EXPECT().Purge(gomock.Any(), int64(9)).Return(errors.New("db"))

		err := svc.Restore(context.Background(), "sha256:aaa")
		So(err, ShouldNotBeNil)
	})
}
