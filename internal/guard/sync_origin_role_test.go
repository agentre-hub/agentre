package guard

import (
	"reflect"
	"testing"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 承载者与最后写者是设备指纹里最容易被当成一回事的两个角色:两列都存着一台机器的
// 指纹,而且同步这条路上它们经常在同一个结构体里紧挨着。
//
//   - 承载者(device_fingerprint / agentred_fingerprint)回答「这东西属于哪台机器」——
//     它是这一行的**身份**,跨机同步时原样搬运,谁都不许改。
//   - 最后写者(sync_origin_fingerprint)回答「哪台机器做了最近这次修改」——
//     它是这一行的**历史**,每写一次就换成写的那一台。
//
// 把承载者写进最后写者那一列,同步冲突的「被谁覆盖了」会指向一台根本没动过这行的
// 机器;反过来,把最后写者写进承载者那一列,一条远端 backend 会在下一次别人改它之后
// 悄悄改换门庭。两边从前都是 string,这两种写法编译器一句都不拦。
//
// 现在它们是两个定义类型,互相赋值编译不过。reflect 的 AssignableTo 实现的就是编译器
// 那条规则。
func TestSyncOriginIsLastWriterNotCarrier(t *testing.T) {
	t.Parallel()

	field, ok := reflect.TypeOf(syncmeta_entity.SyncMeta{}).FieldByName("SyncOriginFingerprint")
	if !ok {
		t.Fatal("SyncMeta 没有字段 SyncOriginFingerprint")
	}

	lastWriter := reflect.TypeOf(devicefp.LastWriter(""))
	carrier := reflect.TypeOf(devicefp.Carrier(""))

	if field.Type != lastWriter {
		t.Fatalf("SyncMeta.SyncOriginFingerprint 的类型是 %s,应当是 devicefp.LastWriter —— "+
			"sync_origin_fingerprint 是冻结的别名列,列名不能改,但读出来的 Go 值必须说出角色",
			field.Type)
	}

	if field.Type.AssignableTo(carrier) || carrier.AssignableTo(field.Type) {
		t.Fatalf("最后写者(%s)与承载者(%s)之间可以直接赋值;"+
			"`SyncOriginFingerprint: row.DeviceFingerprint` 这类写法会重新编译得过",
			field.Type, carrier)
	}
}
