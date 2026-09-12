package guard

import (
	"regexp"
	"strings"
	"testing"
)

// 设备指纹的**值空间只有一个**：桌面端、agentred、server 上那些指纹列彼此逐字节比较
// （pkg/wire/devicefp 的包注释就是这条契约）。一个值空间要有意义，就得让「怎么算」和
// 「存在哪」各只有一处定义——两处定义一样时它们仍会漂移，而漂移的症状不是报错，是同一
// 台机器在 server 上变成两台设备。
//
// 两条规则各钉一份本会漂移的定义：
//
//	格式 `sha256:` + 64 位小写十六进制 —— 只在 pkg/wire/devicefp，那个包的注释就是它的
//	  契约。daemon 用它从 instance UUID 派生，桌面端用它从一份随机实例标识派生：两个种子
//	  来源、一个格式函数，两端因此必然落在同一个值空间里。
//
//	keychain 账号名 —— 只在 internal/pkg/deviceidentity，桌面端指纹的唯一来源。它被 LAN
//	  配对、账号登录、watcher 三处读（还有启动期那处已经删掉）。三份字面量里打错一份，
//	  读到的是**另一个** keychain 条目：Get 拿到 ErrNotFound、于是当场又铸一个指纹。
//
// 只跳过 _test.go，**不跳过 e2e/**：e2e 的 Go 文件不是生产代码（production 入口依赖不到
// 它们，见 production_dependencies_test.go），但它们的活是原样复刻生产的磁盘形状，所以
// 镜像一份常量在那里同样是个入口 —— 那一份就是从这条规则里发现并改掉的。
const (
	deviceFingerprintFormatHome  = "pkg/wire/devicefp/"
	deviceFingerprintAccountHome = "internal/pkg/deviceidentity/"
)

// fingerprintFormatConstruction 匹配**构造**一个指纹：`"sha256:"` 后面紧跟拼接。
//
// 只盯「拼接」是有意的：字符串前缀检查（`strings.HasPrefix(fp, "sha256:")`、
// `strings.TrimPrefix(id, "sha256:")`）是在**问**一个值属不属于这个空间，不是在定义它，
// 别处该问就问。定义只有一处。
var fingerprintFormatConstruction = regexp.MustCompile(`"sha256:"\s*\+`)

// fingerprintAccountLiteral 是 keychain 账号名的字面量。
const fingerprintAccountLiteral = `"agentre-device-fingerprint"`

func TestDeviceFingerprintHasOneFormatAndOneKeychainAccount(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	var (
		scanned     int
		formatHome  int
		accountHome int
		violations  []string
	)

	err := walkRepositoryGoFiles(root, func(rel string, content []byte) error {
		scanned++
		// 只查生产文件。测试里写死账号名/前缀是**在断言契约**（mock 的 EXPECT 参数
		// 就该是那个字面量），那不是第二处定义。
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		if strings.HasPrefix(rel, deviceFingerprintFormatHome) {
			formatHome++
		}
		if strings.HasPrefix(rel, deviceFingerprintAccountHome) {
			accountHome++
		}

		text := string(content)
		if fingerprintFormatConstruction.MatchString(text) && !strings.HasPrefix(rel, deviceFingerprintFormatHome) {
			violations = append(violations, rel+" 自己拼了 `sha256:` 前缀的指纹（应当在 "+deviceFingerprintFormatHome+" 里算）")
		}
		if strings.Contains(text, fingerprintAccountLiteral) && !strings.HasPrefix(rel, deviceFingerprintAccountHome) {
			violations = append(violations, rel+" 自己写了一份 keychain 账号名 "+fingerprintAccountLiteral+"（应当在 "+deviceFingerprintAccountHome+" 里定义）")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 自证不空过。三条各自都能让这个守卫静默全绿：走不到源码、格式的「家」没了、
	// 账号的「家」没了。最后两条尤其要紧——把定义搬走而不改这里，规则就变成一句
	// 「谁都可以拼」。
	if scanned == 0 {
		t.Fatalf("walked %s but found no Go sources; the guard would pass vacuously", root)
	}
	if formatHome == 0 {
		t.Fatalf("没有走到 %s 下的任何生产文件；格式规则的家已经不在了", deviceFingerprintFormatHome)
	}
	if accountHome == 0 {
		t.Fatalf("没有走到 %s 下的任何生产文件；账号规则的家已经不在了", deviceFingerprintAccountHome)
	}

	for _, violation := range violations {
		t.Error(violation)
	}
}
