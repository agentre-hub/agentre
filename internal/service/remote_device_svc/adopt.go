package remote_device_svc

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/model/entity/paired_agentred_entity"
)

// AccountDevice 是账号设备清单里与收编有关的那三个字段。刻意不复用 server_svc 的
// 类型：remote_device_svc 不该反向依赖账号层，调用方负责翻译。
type AccountDevice struct {
	Fingerprint string
	Name        string
	// Kind 是 desktop / agentred。
	Kind string
}

// kindAgentred 与 server 侧 device_entity.KindAgentred 同值。
const kindAgentred = "agentred"

// AdoptAccountDevices 把账号里已有、本机却没有本地记录的 agentred 收编成一行
// **只有中转路径**的记录（URL 为空，见 paired_agentred_entity.IsRelayOnly），
// 返回本次新收编的台数。
//
// 为什么需要它：决策 1 说「账号即信任边界，配对降为『未认领』时的本地路径」，可在此
// 之前只有 LAN 配对握手能产生 paired_agentreds 的行，而整条远端执行链路
// （ConnPool.Borrow / localPairedDeviceID / 后端的「运行设备」）都以这张表的主键寻址。
// 于是账号里信任着、中转上在线着的一台机器，只要本机没亲手配对过，就选不进运行设备；
// 而配对握手必须直连可达，跨网段的机器连补配对的机会都没有。收编补上这一行之后，
// 那把主键钥匙不必动，中转按指纹寻址照常工作。
//
// 四类不收：
//   - 桌面端 —— 它没有执行面，收了会在设备面板凭空多一行、在运行设备里列一台跑不了活的机器。
//   - 本机自己 —— 本机不是「远端」。
//   - 已经有本地记录的指纹 —— 收编按指纹幂等；尤其不能在已有 LAN 配对行之上再加一行，
//     那会让同一台机器变成两台设备、两个连接池 entry、两份在线状态。
//   - 用户亲手解除过配对的指纹 —— 见 tombstonedFingerprints。
func (s *service) AdoptAccountDevices(ctx context.Context, devices []AccountDevice) (int, error) {
	if len(devices) == 0 {
		return 0, nil
	}
	rows, err := s.repo.List(ctx)
	if err != nil {
		return 0, err
	}
	known := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row != nil && row.DaemonFingerprint != "" {
			known[row.DaemonFingerprint] = struct{}{}
		}
	}
	refused, err := s.tombstonedFingerprints(ctx, devices)
	if err != nil {
		return 0, err
	}
	// 本机指纹取不到不是收编的理由：宁可一台都不收，也不能把本机当成远端收进来。
	self, err := s.DeviceFingerprint()
	if err != nil {
		return 0, err
	}

	adopted := 0
	for _, d := range devices {
		fp := strings.TrimSpace(d.Fingerprint)
		if fp == "" || fp == self || d.Kind != kindAgentred {
			continue
		}
		if _, ok := known[fp]; ok {
			continue
		}
		if _, ok := refused[fp]; ok {
			logger.Ctx(ctx).Debug("remote_device_svc.AdoptAccountDevices: honoring the user's removal of this machine",
				zap.String("fingerprint", fp))
			continue
		}
		row := &paired_agentred_entity.PairedAgentred{
			Name:              adoptedName(d, fp),
			DaemonFingerprint: fp,
			TLSMode:           "default",
			PairedAt:          nowMs(),
			Status:            1, // consts.ACTIVE
		}
		if err := row.Check(ctx); err != nil {
			// 收编是后台补齐，不该因为一台机器的脏数据把其余的也拖住。
			logger.Ctx(ctx).Warn("remote_device_svc.AdoptAccountDevices: skipping unusable account device",
				zap.String("fingerprint", fp), zap.Error(err))
			continue
		}
		if err := s.repo.Create(ctx, row); err != nil {
			return adopted, err
		}
		// 把长连状态机拉起来。少了这一步，这一行的 last_seen_at 永远是 0，
		// DeviceView.online 恒 false，「运行设备」下拉按 online 禁用选项——收编来的
		// 机器会永远是灰的，等于白收。启动时的 StartAll 只覆盖存量行，运行时新收的
		// 这一行没有别的东西会管它。
		if s.watcher != nil {
			_ = s.watcher.Start(ctx, row.ID)
		}
		known[fp] = struct{}{}
		adopted++
		logger.Ctx(ctx).Info("remote_device_svc.AdoptAccountDevices: adopted an account agentred as relay-only",
			zap.String("fingerprint", fp), zap.Int64("deviceID", row.ID))
	}
	return adopted, nil
}

// tombstonedFingerprints 交出「用户亲手把这台机器从本机拿掉过」的那些指纹。
//
// 为什么需要它：收编只增不减、按指纹幂等，于是用户在设备面板点「解除配对」删掉一行
// （remove.go 软删）之后，下一次窗口 focus 触发的设备清单刷新会按指纹发现「本机没有
// 这一行」并重建 —— 删除因此看起来像随机失效：删掉了，切个窗口回来它又在了。
// 软删行还留在表里，它就是删除意图本身，也是唯一一份持久证据。
//
// 判据取「同指纹的软删行」，不取别的：
//   - 它精确到机器。没有软删行的指纹一概照收，「用户从没删过、只是还没收编」的机器
//     不会被误挡（这是本判据的硬要求）。
//   - 空指纹的软删行一律不作数：拿空串当键会把所有还没握过手的旧配对混成同一台，
//     进而永久挡住一台跟它毫无关系的机器。收编建的行必定带指纹（entity.Check 强制），
//     所以「收编来的行被删掉」这件事一定留得下可用的键。
//   - 它挡不住显式的本地动作：Add 只看存活行（FindByURL / FindByFingerprint 都限
//     status=ACTIVE），部分唯一索引也只覆盖存活行，所以用户随时可以再 LAN 配对一次
//     把这台机器请回来。
//
// 墓碑不是永久的：它记的是「我不要这台机器出现在这台桌面上」，绑在**这一次账号归属**
// 上。中转收编来的那种墓碑（IsRelayOnly，只可能由本方法的收编产生）一旦对应的机器
// 整个不在账号清单里了，这次归属就结束了，墓碑随之回收 —— 那台机器下次重新
// agentred login 回到账号是一次新的加入，不该被上一次的拒绝一直挡着。
// 手工 LAN 配对过又解除的墓碑不回收：它本来就可能从来不属于任何账号，账号清单里没有
// 它不说明任何事情，它的重新进入通路是再配对一次。
//
// 回收失败只记日志：它是清理，不该让整轮收编失败。
func (s *service) tombstonedFingerprints(
	ctx context.Context, devices []AccountDevice,
) (map[string]struct{}, error) {
	deleted, err := s.repo.ListDeleted(ctx)
	if err != nil {
		return nil, err
	}
	inAccount := make(map[string]struct{}, len(devices))
	for _, d := range devices {
		if fp := strings.TrimSpace(d.Fingerprint); fp != "" {
			inAccount[fp] = struct{}{}
		}
	}
	refused := make(map[string]struct{}, len(deleted))
	for _, row := range deleted {
		if row == nil {
			continue
		}
		fp := strings.TrimSpace(row.DaemonFingerprint)
		if fp == "" {
			continue
		}
		if _, still := inAccount[fp]; !still && row.IsRelayOnly() {
			if perr := s.repo.Purge(ctx, row.ID); perr != nil {
				logger.Ctx(ctx).Warn("remote_device_svc.AdoptAccountDevices: retiring a stale tombstone failed",
					zap.Int64("deviceID", row.ID), zap.Error(perr))
				refused[fp] = struct{}{}
			}
			continue
		}
		refused[fp] = struct{}{}
	}
	return refused, nil
}

// adoptedName 优先用账号侧登记的机器名；它没名字时回落到指纹缩写，绝不留空
// （空名字过不了 entity.Check，而一台连不上名字的机器在列表里没法被指认）。
func adoptedName(d AccountDevice, fingerprint string) string {
	if name := strings.TrimSpace(d.Name); name != "" {
		return name
	}
	const shortLen = 12
	trimmed := strings.TrimPrefix(fingerprint, "sha256:")
	if len(trimmed) > shortLen {
		trimmed = trimmed[:shortLen]
	}
	return "agentred-" + trimmed
}
