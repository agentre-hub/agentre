package guard

import (
	"reflect"
	"testing"

	"github.com/agentre-hub/agentre/internal/daemon/repository/session_repo"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 承载者与发起方的区别是文档里点名「最咬人」的那一对,而 daemon 这一侧是它在本仓
// 最清楚的形状:
//
//   - daemon_sessions.peer_fingerprint 是**发起方** —— 把这条对话交到本机执行的那
//     一端。它可能是一台桌面端,也可能是 web 控制台的中继身份,而后者**根本不在账号
//     的设备列表里**。
//   - chat_sessions.exec_device_fingerprint 是**承载者** —— 这条会话实际跑在哪台
//     机器上。
//
// 拿承载者去查 daemon_sessions 是错的:daemon 自己的指纹永远不会等于任何一条会话的
// peer_fingerprint,查出来恒空;反过来拿发起方去配对表里解析机器名/在线状态同样是错的
// —— 浏览器那种发起方压根没有配对行,只会解析成一台没名字的离线机器(be367651 那条
// 界面症状的同一个成因)。
//
// 两边从前都是 string,这两种写法编译器一句都不拦。现在它们是两个定义类型。
func TestPeerFingerprintIsInitiatorNotCarrier(t *testing.T) {
	t.Parallel()

	initiator := reflect.TypeOf(devicefp.Initiator(""))
	carrier := reflect.TypeOf(devicefp.Carrier(""))

	peer, ok := reflect.TypeOf(session_repo.DaemonSession{}).FieldByName("PeerFingerprint")
	if !ok {
		t.Fatal("DaemonSession 没有字段 PeerFingerprint")
	}
	if peer.Type != initiator {
		t.Fatalf("DaemonSession.PeerFingerprint 的类型是 %s,应当是 devicefp.Initiator", peer.Type)
	}

	exec, ok := reflect.TypeOf(chat_entity.Session{}).FieldByName("ExecDeviceFingerprint")
	if !ok {
		t.Fatal("chat_entity.Session 没有字段 ExecDeviceFingerprint")
	}
	if exec.Type != carrier {
		t.Fatalf("Session.ExecDeviceFingerprint 的类型是 %s,应当是 devicefp.Carrier", exec.Type)
	}

	if peer.Type.AssignableTo(exec.Type) || exec.Type.AssignableTo(peer.Type) {
		t.Fatalf("发起方(%s)与承载者(%s)之间可以直接赋值;"+
			"拿本机承载者指纹去查 daemon_sessions.peer_fingerprint 会重新编译得过",
			peer.Type, exec.Type)
	}
}

// 中转拨号那条路上的 peerFingerprint 也是发起方:它是「本端在这条通道上出示的身份」,
// 由对端记成会话的发起方。它与同一次调用里的 daemonFingerprint(要连的是**哪台机器**,
// 承载者)是同一个函数的两个相邻参数 —— 这正是最容易顺手写反的形状,而写反的后果是
// 拨给自己、或者把对端的机器身份当成发起方记进会话。
func TestRelayDialSeparatesTargetMachineFromCallingPeer(t *testing.T) {
	t.Parallel()

	open, ok := reflect.TypeOf((*remote_device_svc.RelayDialPort)(nil)).Elem().MethodByName("Open")
	if !ok {
		t.Fatal("RelayDialPort 没有方法 Open")
	}

	// Open(ctx, daemonFingerprint, peerFingerprint)
	target, caller := open.Type.In(1), open.Type.In(2)
	if want := reflect.TypeOf(devicefp.Carrier("")); target != want {
		t.Errorf("Open 的目标机器参数是 %s,应当是 %s", target, want)
	}
	if want := reflect.TypeOf(devicefp.Initiator("")); caller != want {
		t.Errorf("Open 的本端身份参数是 %s,应当是 %s —— 它是发起方,不是要连的那台机器", caller, want)
	}
	if target.AssignableTo(caller) || caller.AssignableTo(target) {
		t.Fatal("Open 的两个指纹参数之间可以直接赋值;把它们写反编译器不会拦")
	}
}
