package devicefp_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// roles 列出四个角色各一个零值，顺序与 docs/architecture.md 的角色表一致。
func roles() map[string]any {
	return map[string]any{
		"Carrier":    devicefp.Carrier(""),
		"Initiator":  devicefp.Initiator(""),
		"LastWriter": devicefp.LastWriter(""),
		"Client":     devicefp.Client(""),
	}
}

// TestRolesAreMutuallyUnassignable 是这个包存在的理由：四个角色共用一个值空间，
// 混用两个是真 bug。reflect 的 AssignableTo 实现的就是编译器那条赋值规则，所以
// 「两两不可赋值」等价于「写错了编译不过」。
func TestRolesAreMutuallyUnassignable(t *testing.T) {
	t.Parallel()

	all := roles()
	for fromName, from := range all {
		for toName, to := range all {
			if fromName == toName {
				continue
			}
			ft, tt := reflect.TypeOf(from), reflect.TypeOf(to)
			if ft.AssignableTo(tt) {
				t.Errorf("%s 可以直接赋给 %s；角色混用不再是编译错误，这个包就失去意义了", fromName, toName)
			}
		}
	}
}

// TestRawStringIsNotAssignableToAnyRole 守的是边界：protobuf 生成的是裸 string，
// 数据库列也是。裸串必须在边界上被显式声明成某个角色，不能悄悄流进来。
func TestRawStringIsNotAssignableToAnyRole(t *testing.T) {
	t.Parallel()

	raw := reflect.TypeOf("")
	for name, role := range roles() {
		rt := reflect.TypeOf(role)
		if raw.AssignableTo(rt) {
			t.Errorf("裸 string 可以直接赋给 %s；边界上就不会有人被迫说出角色了", name)
		}
		if rt.AssignableTo(raw) {
			t.Errorf("%s 可以直接赋给裸 string；角色会在出口处静默丢失", name)
		}
	}
}

// TestRolesStayStringsOnTheWire 钉住取值形态：它们只是被命名的 string，
// JSON / 数据库 / protobuf 上的字节与从前完全一样，换类型不改任何线格式。
func TestRolesStayStringsOnTheWire(t *testing.T) {
	t.Parallel()

	for name, role := range roles() {
		if k := reflect.TypeOf(role).Kind(); k != reflect.String {
			t.Fatalf("%s 的底层类型是 %s，不是 string", name, k)
		}
	}

	b, err := json.Marshal(struct {
		FP devicefp.Carrier `json:"fp"`
	}{FP: "sha256:abc"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(b), `{"fp":"sha256:abc"}`; got != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}

// TestConversionBetweenRolesIsExplicit 记录允许的那一半：同一个值确实会换角色
// （对端交过来的 initiator 指纹，在本机就是那台机器的 carrier），这时必须写出
// 一次显式转换 —— 少而集中，且在 diff 里看得见。
func TestConversionBetweenRolesIsExplicit(t *testing.T) {
	t.Parallel()

	initiator := devicefp.Initiator("sha256:abc")
	carrier := devicefp.Carrier(initiator)
	if carrier != "sha256:abc" {
		t.Errorf("carrier = %q", carrier)
	}
}
