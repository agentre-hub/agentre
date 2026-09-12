package deviceidentity_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/deviceidentity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// canonical 就地把「规范指纹长什么样」写出来：`sha256:` + 64 位小写十六进制。不去问
// devicefp 要一个校验函数 —— 这条用例钉的就是那条契约，从被验方借尺子就测不出来了。
func canonical(t *testing.T, fp devicefp.Carrier) {
	t.Helper()

	if !strings.HasPrefix(string(fp), "sha256:") {
		t.Fatalf("指纹少了 `sha256:` 前缀：%q", fp)
	}
	body := strings.TrimPrefix(string(fp), "sha256:")
	if len(body) != 64 {
		t.Fatalf("指纹的十六进制长度是 %d，应当是 64：%q", len(body), fp)
	}
	for _, r := range body {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("指纹里有非小写十六进制字符 %q：%q", r, fp)
		}
	}
}

// 铸出来的指纹必须**留下来**：它是本机身份，第二次问要拿到同一个值。没有这条性质，
// 每次冷启动都会在 server 上多出一台设备。
func TestEnsure_GivenAnEmptyKeychain_ThenMintsACanonicalFingerprintAndKeepsIt(t *testing.T) {
	t.Parallel()

	kc := keychain.NewMemory()
	first, err := deviceidentity.Ensure(kc)
	if err != nil {
		t.Fatal(err)
	}
	canonical(t, first)

	second, err := deviceidentity.Ensure(kc)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("第二次读到 %q，与铸出来的 %q 不同：指纹没有被持久化", second, first)
	}

	stored, err := kc.Get(deviceidentity.KeychainAccount)
	if err != nil {
		t.Fatal(err)
	}
	if stored != string(first) {
		t.Fatalf("keychain 里存的是 %q，索引方拿到的是 %q", stored, first)
	}
}

// 已经有值就照原样用，不覆盖。这一条是「R5 决策 8：账号侧不得另生成指纹」的落点 ——
// LAN 配对先铸的指纹，账号登录必须复用，否则同一台机器在本地有两个身份。
func TestEnsure_GivenAnExistingFingerprint_ThenReusesIt(t *testing.T) {
	t.Parallel()

	kc := keychain.NewMemory()
	const existing = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := kc.Set(deviceidentity.KeychainAccount, existing); err != nil {
		t.Fatal(err)
	}

	fp, err := deviceidentity.Ensure(kc)
	if err != nil {
		t.Fatal(err)
	}
	if string(fp) != existing {
		t.Fatalf("Ensure 返回 %q，应当复用已有的 %q", fp, existing)
	}
	stored, err := kc.Get(deviceidentity.KeychainAccount)
	if err != nil {
		t.Fatal(err)
	}
	if stored != existing {
		t.Fatalf("已有值被改写成了 %q", stored)
	}
}

// keychain 是真出错了（后端不可用、权限被拒）时不能顺手铸一个新的：那会在一个读不到的
// 身份之上再叠一个身份。只有 ErrNotFound 才等于「还没有」。
func TestEnsure_GivenAKeychainFailure_ThenPropagatesAndMintsNothing(t *testing.T) {
	t.Parallel()

	boom := errors.New("keychain backend unavailable")
	kc := &failingKeychain{err: boom}

	if _, err := deviceidentity.Ensure(kc); !errors.Is(err, boom) {
		t.Fatalf("Ensure 返回 %v，应当原样传出 %v", err, boom)
	}
	if kc.set {
		t.Fatal("读失败时仍然往 keychain 里写了东西")
	}
}

// nil 是「没有 keychain」而不是「keychain 里没有值」：前者要报错，不能悄悄返回空串。
func TestEnsure_GivenNilKeychain_ThenError(t *testing.T) {
	t.Parallel()

	if _, err := deviceidentity.Ensure(nil); err == nil {
		t.Fatal("nil keychain 应当报错，而不是返回空指纹")
	}
}

// failingKeychain 只覆盖失败那一条路：Get 恒定出错，Set 记账。
type failingKeychain struct {
	err error
	set bool
}

func (k *failingKeychain) Get(string) (string, error) { return "", k.err }

func (k *failingKeychain) Set(string, string) error {
	k.set = true
	return nil
}

func (k *failingKeychain) Delete(string) error { return nil }
