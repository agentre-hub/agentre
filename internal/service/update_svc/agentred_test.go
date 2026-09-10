package update_svc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentredArchive 造一个与 `make agentred-package` 同形的发布资产：tar.gz 根目录下
// 一个名为 agentred 的可执行文件。
func agentredArchive(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "agentred",
		Mode:     0o755,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// agentredReleaseServer 提供一个 AGENTRED_RELEASE_BASE_URL 形态的发布目录：
// SHA256SUMS 加若干资产，与 scripts/install.sh 读的是同一份布局。
type agentredReleaseServer struct {
	*httptest.Server
	requests []string
}

func newAgentredReleaseServer(t *testing.T, files map[string][]byte) *agentredReleaseServer {
	t.Helper()
	srv := &agentredReleaseServer{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.requests = append(srv.requests, r.URL.Path)
		body, ok := files[r.URL.Path[1:]]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func checksumFile(entries map[string][]byte) []byte {
	var buf bytes.Buffer
	for name, body := range entries {
		fmt.Fprintf(&buf, "%s  %s\n", sha256Hex(body), name)
	}
	return buf.Bytes()
}

func TestNormalizeAgentredChannel(t *testing.T) {
	convey.Convey("通道解析", t, func() {
		convey.Convey("空串默认 stable", func() {
			channel, err := NormalizeChannel("")
			assert.NoError(t, err)
			assert.Equal(t, ChannelStable, channel)
		})

		convey.Convey("三个通道都认", func() {
			for _, want := range []string{ChannelStable, ChannelBeta, ChannelNightly} {
				channel, err := NormalizeChannel(want)
				assert.NoError(t, err)
				assert.Equal(t, want, channel)
			}
		})

		convey.Convey("其它值报错并列出可选项", func() {
			_, err := NormalizeChannel("edge")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "edge")
			assert.Contains(t, err.Error(), ChannelNightly)
		})
	})
}

func TestPickAgentredAsset(t *testing.T) {
	assets := []ReleaseAsset{
		{Name: "agentre-0.2.0-linux-amd64.tar.gz"},
		{Name: "agentred-0.2.0-linux-arm64.tar.gz"},
		{Name: "agentred-0.2.0-linux-amd64.tar.gz"},
		{Name: "agentred-0.2.0-windows-amd64.zip"},
		{Name: "SHA256SUMS.txt"},
	}
	convey.Convey("资产选择", t, func() {
		convey.Convey("按平台选 agentred 的归档，不会选中桌面端同名平台的资产", func() {
			name, version := pickAgentredAsset(assetNames(assets), "linux", "amd64")
			assert.Equal(t, "agentred-0.2.0-linux-amd64.tar.gz", name)
			assert.Equal(t, "0.2.0", version)
		})

		convey.Convey("Windows 选 zip", func() {
			name, _ := pickAgentredAsset(assetNames(assets), "windows", "amd64")
			assert.Equal(t, "agentred-0.2.0-windows-amd64.zip", name)
		})

		convey.Convey("当前平台没有资产时空手而归", func() {
			name, _ := pickAgentredAsset(assetNames(assets), "darwin", "arm64")
			assert.Equal(t, "", name)
		})
	})
}

func TestResolveAgentredReleaseFromBaseURL(t *testing.T) {
	convey.Convey("AGENTRED_RELEASE_BASE_URL 覆盖下的发布解析", t, func() {
		archive := agentredArchive(t, "new binary")
		files := map[string][]byte{
			"agentred-0.2.0-linux-amd64.tar.gz": archive,
		}
		files["SHA256SUMS"] = checksumFile(files)

		convey.Convey("报出最新版本、资产地址与校验值", func() {
			server := newAgentredReleaseServer(t, files)
			release, err := ResolveAgentredRelease(context.Background(), AgentredReleaseOptions{
				BaseURL:        server.URL,
				CurrentVersion: "0.1.0",
				GOOS:           "linux",
				GOARCH:         "amd64",
			})
			require.NoError(t, err)
			assert.Equal(t, "0.2.0", release.LatestVersion)
			assert.Equal(t, "0.1.0", release.CurrentVersion)
			assert.True(t, release.HasUpdate)
			assert.Equal(t, "agentred-0.2.0-linux-amd64.tar.gz", release.AssetName)
			assert.Equal(t, server.URL+"/agentred-0.2.0-linux-amd64.tar.gz", release.AssetURL)
			assert.Equal(t, sha256Hex(archive), release.SHA256)
		})

		convey.Convey("已是最新时 HasUpdate 为假", func() {
			server := newAgentredReleaseServer(t, files)
			release, err := ResolveAgentredRelease(context.Background(), AgentredReleaseOptions{
				BaseURL:        server.URL,
				CurrentVersion: "0.2.0",
				GOOS:           "linux",
				GOARCH:         "amd64",
			})
			require.NoError(t, err)
			assert.False(t, release.HasUpdate)
		})

		convey.Convey("当前平台缺资产是可断言的错误，且没有去下载任何东西", func() {
			server := newAgentredReleaseServer(t, files)
			_, err := ResolveAgentredRelease(context.Background(), AgentredReleaseOptions{
				BaseURL:        server.URL,
				CurrentVersion: "0.1.0",
				GOOS:           "darwin",
				GOARCH:         "arm64",
			})
			var missing *AssetNotFoundError
			require.ErrorAs(t, err, &missing)
			assert.Equal(t, "darwin-arm64", missing.Platform)
			assert.Equal(t, []string{"/SHA256SUMS"}, server.requests)
		})

		convey.Convey("发布源不可达时提示可以改 AGENTRED_RELEASE_BASE_URL", func() {
			server := newAgentredReleaseServer(t, nil)
			_, err := ResolveAgentredRelease(context.Background(), AgentredReleaseOptions{
				BaseURL:        server.URL,
				CurrentVersion: "0.1.0",
				GOOS:           "linux",
				GOARCH:         "amd64",
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "AGENTRED_RELEASE_BASE_URL")
		})
	})
}

func TestApplyAgentredUpdate(t *testing.T) {
	convey.Convey("执行升级", t, func() {
		archive := agentredArchive(t, "new binary")
		files := map[string][]byte{"agentred-0.2.0-linux-amd64.tar.gz": archive}
		files["SHA256SUMS"] = checksumFile(files)

		newTarget := func(t *testing.T) string {
			t.Helper()
			dir := t.TempDir()
			target := filepath.Join(dir, "agentred")
			require.NoError(t, os.WriteFile(target, []byte("old binary"), 0o755))
			return target
		}
		resolve := func(t *testing.T, server *agentredReleaseServer) *AgentredRelease {
			t.Helper()
			release, err := ResolveAgentredRelease(context.Background(), AgentredReleaseOptions{
				BaseURL:        server.URL,
				CurrentVersion: "0.1.0",
				GOOS:           "linux",
				GOARCH:         "amd64",
			})
			require.NoError(t, err)
			return release
		}

		convey.Convey("校验通过后原子替换目标文件", func() {
			server := newAgentredReleaseServer(t, files)
			target := newTarget(t)
			require.NoError(t, ApplyAgentredUpdate(context.Background(), resolve(t, server),
				ApplyAgentredUpdateOptions{TargetPath: target}))
			content, err := os.ReadFile(target) //nolint:gosec // G304: target 来自 t.TempDir()。
			require.NoError(t, err)
			assert.Equal(t, "new binary", string(content))
			info, err := os.Stat(target)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
		})

		convey.Convey("校验和不符时报错，且原二进制不动", func() {
			server := newAgentredReleaseServer(t, files)
			target := newTarget(t)
			release := resolve(t, server)
			release.SHA256 = "00000000000000000000000000000000000000000000000000000000000000ff"
			err := ApplyAgentredUpdate(context.Background(), release,
				ApplyAgentredUpdateOptions{TargetPath: target})
			var mismatch *ChecksumMismatchError
			require.ErrorAs(t, err, &mismatch)
			assert.Equal(t, "agentred-0.2.0-linux-amd64.tar.gz", mismatch.AssetName)
			content, readErr := os.ReadFile(target) //nolint:gosec // G304: target 来自 t.TempDir()。
			require.NoError(t, readErr)
			assert.Equal(t, "old binary", string(content))
			entries, readErr := os.ReadDir(filepath.Dir(target))
			require.NoError(t, readErr)
			assert.Len(t, entries, 1, "不留半个二进制")
		})

		convey.Convey("资产下载不到时报错，且原二进制不动", func() {
			server := newAgentredReleaseServer(t, files)
			target := newTarget(t)
			release := resolve(t, server)
			release.AssetURL = server.URL + "/agentred-0.2.0-linux-amd64-missing.tar.gz"
			err := ApplyAgentredUpdate(context.Background(), release,
				ApplyAgentredUpdateOptions{TargetPath: target})
			require.Error(t, err)
			content, readErr := os.ReadFile(target) //nolint:gosec // G304: target 来自 t.TempDir()。
			require.NoError(t, readErr)
			assert.Equal(t, "old binary", string(content))
			entries, readErr := os.ReadDir(filepath.Dir(target))
			require.NoError(t, readErr)
			assert.Len(t, entries, 1, "不留半个二进制")
		})

		convey.Convey("目标目录不可写时指名道姓地报错，且一个字节都没下载", func() {
			if os.Geteuid() == 0 {
				t.Skip("root 无视目录权限位，这条判定在 root 下测不出来")
			}
			server := newAgentredReleaseServer(t, files)
			target := newTarget(t)
			release := resolve(t, server)
			before := len(server.requests)
			dir := filepath.Dir(target)
			require.NoError(t, os.Chmod(dir, 0o555))
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

			err := ApplyAgentredUpdate(context.Background(), release,
				ApplyAgentredUpdateOptions{TargetPath: target})
			var unwritable *TargetNotWritableError
			require.ErrorAs(t, err, &unwritable)
			assert.Equal(t, target, unwritable.Path)
			assert.Len(t, server.requests, before, "不可写就不该下载")
			content, readErr := os.ReadFile(target) //nolint:gosec // G304: target 来自 t.TempDir()。
			require.NoError(t, readErr)
			assert.Equal(t, "old binary", string(content))
		})
	})
}

func TestGuardActiveTurns(t *testing.T) {
	convey.Convey("活跃轮次闸门", t, func() {
		convey.Convey("没有轮次在跑时放行", func() {
			assert.NoError(t, GuardActiveTurns(0, false))
		})

		convey.Convey("有轮次在跑时按条数拒绝", func() {
			err := GuardActiveTurns(2, false)
			var active *ActiveTurnsError
			require.ErrorAs(t, err, &active)
			assert.Equal(t, int64(2), active.Count)
			assert.Contains(t, err.Error(), "2")
		})

		convey.Convey("显式越过时放行", func() {
			assert.NoError(t, GuardActiveTurns(2, true))
		})
	})
}

func TestAgentredErrorsAreDistinguishable(t *testing.T) {
	convey.Convey("四种拒绝理由互不冒充", t, func() {
		var (
			active     *ActiveTurnsError
			unwritable *TargetNotWritableError
			mismatch   *ChecksumMismatchError
			missing    *AssetNotFoundError
		)
		assert.False(t, errors.As(error(&ActiveTurnsError{Count: 1}), &unwritable))
		assert.False(t, errors.As(error(&TargetNotWritableError{Path: "/x"}), &active))
		assert.False(t, errors.As(error(&ChecksumMismatchError{AssetName: "a"}), &missing))
		assert.False(t, errors.As(error(&AssetNotFoundError{Platform: "p"}), &mismatch))
	})
}
