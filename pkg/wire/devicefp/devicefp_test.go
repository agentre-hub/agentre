package devicefp_test

import (
	"encoding/json"
	"reflect"
	"strings"
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

// FromSeed 的两个性质就是这个值空间的两条规则：**确定性**（同一个种子永远同一个值，
// 否则重启一次设备就换了个身份）与**格式**（`sha256:` + 64 位小写十六进制，否则两端
// 生成的指纹逐字节比不相等）。
//
// 向量取自 sha256("abc123") —— 与 agentred 那一侧的派生同源的既有行为，换实现不能换值。
func TestFromSeed_GivenASeed_ThenTheCanonicalSHA256Fingerprint(t *testing.T) {
	t.Parallel()

	got := devicefp.FromSeed("abc123")
	const want = devicefp.Carrier("sha256:6ca13d52ca70c883e0f0bb101e425a89e8624de51db2d2392593af6a84118090")
	if got != want {
		t.Fatalf("FromSeed(\"abc123\") = %q, want %q", got, want)
	}
	if again := devicefp.FromSeed("abc123"); again != got {
		t.Fatalf("同一个种子两次派生得到不同值：%q / %q", got, again)
	}
}

// 桌面端的种子是 32 字节随机数**按字节**交给这里的（string 只是字节容器），所以种子
// 里出现非 UTF-8 字节是常态而不是边界情况。格式必须与种子长什么样无关。
func TestFromSeed_GivenANonUTF8Seed_ThenStillTheCanonicalShape(t *testing.T) {
	t.Parallel()

	seed := string([]byte{0xff, 0x00, 0xfe, 0x80})
	fp := devicefp.FromSeed(seed)

	body := strings.TrimPrefix(string(fp), "sha256:")
	if body == string(fp) {
		t.Fatalf("FromSeed 少了 `sha256:` 前缀：%q", fp)
	}
	if len(body) != 64 {
		t.Fatalf("FromSeed 的十六进制长度是 %d，应当是 64：%q", len(body), fp)
	}
	for _, r := range body {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("FromSeed 里出现了非小写十六进制字符 %q：%q", r, fp)
		}
	}
	if devicefp.FromSeed(string([]byte{0xff, 0x00, 0xfe, 0x81})) == fp {
		t.Fatal("两个不同的种子派生出同一个指纹")
	}
}
