package remote_device_svc

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// 「同步到设备」(把本机供应商连 key 推给某台 agentred)取消了:账号供应商的 key 只经租约
// 在 agentred 内存里拿,直连派发由这一轮附带。服务与借出的连接上都不再有推供应商的口子。
func TestRemoteDeviceSvc_HasNoWayToPushAProviderKeyToADevice(t *testing.T) {
	_, onService := reflect.TypeOf((*RemoteDeviceSvc)(nil)).Elem().MethodByName("SyncProvider")
	assert.False(t, onService, "RemoteDeviceSvc.SyncProvider is gone")
	_, onLease := reflect.TypeOf((*Lease)(nil)).Elem().MethodByName("LLMUpsert")
	assert.False(t, onLease, "Lease.LLMUpsert is gone")
}
