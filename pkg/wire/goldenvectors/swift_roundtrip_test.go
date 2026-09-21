package goldenvectors_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/pkg/wire/goldenvectors"
)

// swiftPackageDir 是本 module 里那个 SwiftPM 包(生成的 Swift 产物 + 它的测试)。
const swiftPackageDir = "../swift"

// roundTripEnv 是 Swift 测试把"它自己编码回去的字节"写到哪里。
// 由这里给出临时目录,Swift 侧不选路径,产物因此永远不会落进仓库。
const roundTripEnv = "WIRE_SWIFT_ROUNDTRIP_OUT"

// TestSwiftDecodesAndReEncodesVectors 是本包存在的理由:同一份 schema 的 Swift 实现
// 与 Go 实现对同一批帧同解同编。
//
// 它由 Go 这一侧驱动,因为对拍的后半程只有 Go 做得了:Swift 断言完字段后把消息再编码
// 回去,得到的字节要由 Go 解回来、与原消息 proto.Equal。两个进程各解一次、各编一次,
// 任何一侧把字段读漏、把未设的 oneof 读成某个分支、或把默认值写上线,都在这里变红。
//
// 判据是 proto.Equal 而不是字节相等:protobuf 不保证跨实现的字节级一致(map 序、
// 未知字段的摆放),拿字节做判据只会得到一条随实现升级而红的测试。
func TestSwiftDecodesAndReEncodesVectors(t *testing.T) {
	swift, err := exec.LookPath("swift")
	if err != nil {
		t.Skip("PATH 上没有 swift,跳过跨语言对拍;有 Swift 工具链的机器上它会真的跑")
	}

	out := t.TempDir()
	absOut, err := filepath.Abs(out)
	require.NoError(t, err)

	// SwiftPM 首次要解析依赖并编译,没有上限时一次网络卡顿就变成一条永远挂着的测试。
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	//nolint:gosec // swift 来自 LookPath,参数是本文件里的常量,没有一个来自外部输入。
	cmd := exec.CommandContext(ctx, swift, "test", "--package-path", swiftPackageDir)
	cmd.Env = append(os.Environ(), roundTripEnv+"="+absOut)
	combined, runErr := cmd.CombinedOutput()
	require.NoError(t, runErr, "swift test 失败:\n%s", combined)

	// Swift 每解编一条向量就写一个同名 .bin。文件集必须与向量集完全一致 ——
	// 少写一条就是那条向量根本没被 Swift 跑到,而测试会一路绿到底。
	vectors := goldenvectors.Vectors()
	produced := listNames(t, absOut)
	require.Len(t, produced, len(vectors),
		"Swift 侧回写的向量数与 Go 侧的向量集不一致:\n%s", combined)

	for _, v := range vectors {
		//nolint:gosec // absOut 是本测试自己的 t.TempDir(),文件名来自本包的向量集。
		reencoded, err := os.ReadFile(filepath.Join(absOut, v.Name+".bin"))
		require.NoError(t, err, "Swift 没有回写向量 %s", v.Name)

		back := v.Message.ProtoReflect().New().Interface()
		require.NoError(t, proto.Unmarshal(reencoded, back),
			"Go 解不开 Swift 为向量 %s 编码出来的字节", v.Name)
		require.True(t, proto.Equal(v.Message, back),
			"向量 %s 经 Swift 解编一轮后与原消息不等\n原:  %v\n回:  %v", v.Name, v.Message, back)
	}
}
