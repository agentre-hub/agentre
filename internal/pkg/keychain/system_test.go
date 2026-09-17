package keychain_test

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/zalando/go-keyring"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

// fakeKeyringBackend 是一个按 (service, account) 分槽的内存桩，模拟真实 OS keychain 对
// 不同 service 严格隔离的行为。它替换掉 go-keyring 的包级函数，保证这些用例永不触碰真实
// 系统钥匙串。
type fakeKeyringBackend struct {
	slots map[[2]string]string
}

func newFakeKeyringBackend() *fakeKeyringBackend {
	return &fakeKeyringBackend{slots: map[[2]string]string{}}
}

func (f *fakeKeyringBackend) get(service, account string) (string, error) {
	v, ok := f.slots[[2]string{service, account}]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeKeyringBackend) set(service, account, secret string) error {
	f.slots[[2]string{service, account}] = secret
	return nil
}

func (f *fakeKeyringBackend) delete(service, account string) error {
	key := [2]string{service, account}
	if _, ok := f.slots[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.slots, key)
	return nil
}

func TestSystemKeychainUsesGivenServiceSlot(t *testing.T) {
	Convey("NewSystem(service) reads and writes only that service's slot", t, func() {
		backend := newFakeKeyringBackend()
		keychain.SetSystemKeyringForTest(t, backend.get, backend.set, backend.delete)

		beta := keychain.NewSystem("agentre-beta")
		So(beta.Set("acc", "beta-secret"), ShouldBeNil)

		v, ok := backend.slots[[2]string{"agentre-beta", "acc"}]
		So(ok, ShouldBeTrue)
		So(v, ShouldEqual, "beta-secret")

		got, err := beta.Get("acc")
		So(err, ShouldBeNil)
		So(got, ShouldEqual, "beta-secret")
	})

	Convey("four channel services never share a slot", t, func() {
		backend := newFakeKeyringBackend()
		keychain.SetSystemKeyringForTest(t, backend.get, backend.set, backend.delete)

		services := []string{"agentre", "agentre-beta", "agentre-nightly", "agentre-dev"}
		for _, svc := range services {
			So(keychain.NewSystem(svc).Set("acc", "secret-for-"+svc), ShouldBeNil)
		}

		for _, svc := range services {
			got, err := keychain.NewSystem(svc).Get("acc")
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "secret-for-"+svc)
		}
		So(len(backend.slots), ShouldEqual, len(services))
	})

	Convey("Get on a missing account maps the backend's not-found error to ErrNotFound", t, func() {
		backend := newFakeKeyringBackend()
		keychain.SetSystemKeyringForTest(t, backend.get, backend.set, backend.delete)

		_, err := keychain.NewSystem("agentre-nightly").Get("missing")
		So(err, ShouldEqual, keychain.ErrNotFound)
	})

	Convey("Delete removes only the targeted service's slot", t, func() {
		backend := newFakeKeyringBackend()
		keychain.SetSystemKeyringForTest(t, backend.get, backend.set, backend.delete)

		So(keychain.NewSystem("agentre").Set("acc", "s1"), ShouldBeNil)
		So(keychain.NewSystem("agentre-beta").Set("acc", "s2"), ShouldBeNil)

		So(keychain.NewSystem("agentre").Delete("acc"), ShouldBeNil)

		_, err := keychain.NewSystem("agentre").Get("acc")
		So(err, ShouldEqual, keychain.ErrNotFound)

		v, err := keychain.NewSystem("agentre-beta").Get("acc")
		So(err, ShouldBeNil)
		So(v, ShouldEqual, "s2")
	})
}
