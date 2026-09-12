package update_svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// agentred 的自更新与桌面端共用同一套「解析发布 → 下载 → 校验 → 替换」的判定，
// 但装的是另一件东西：一个裸二进制，没有 .app bundle、没有安装器，且替换的目标
// 可能正是此刻在跑的 daemon。这里只加 agentred 需要的入口，不改桌面端既有路径。

// AgentredReleaseBaseURLEnv 是 scripts/install.sh 已在用的换源变量名。命令行与
// 安装脚本共用它，内网部署不必学第二套约定。
const AgentredReleaseBaseURLEnv = "AGENTRED_RELEASE_BASE_URL"

// agentredBinaryName 是发布归档里那个可执行文件的名字。
const agentredBinaryName = "agentred"

// AgentredReleaseOptions 描述一次发布解析要看的东西。除 Channel 外都可留空，
// 留空时取当前进程的构建与平台。
type AgentredReleaseOptions struct {
	// Channel 目标通道，空串按 stable。
	Channel string
	// BaseURL 指向一个「SHA256SUMS 加同目录资产」的发布源（AGENTRED_RELEASE_BASE_URL）。
	// 非空时只认这一个源，不再走 GitHub 与内置镜像。
	BaseURL string
	// CurrentVersion 用于比较的当前版本，空串取 configs.Version。
	CurrentVersion string
	// GOOS / GOARCH 目标平台，空串取当前进程的平台。
	GOOS   string
	GOARCH string
}

// AgentredRelease 是一次发布解析的结果：够判断「要不要升」，也够直接下载。
type AgentredRelease struct {
	Channel        string `json:"channel"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	HasUpdate      bool   `json:"hasUpdate"`
	AssetName      string `json:"assetName"`
	// AssetURL 已经套过镜像前缀，可以直接下载。
	AssetURL   string `json:"assetURL"`
	SHA256     string `json:"sha256"`
	ReleaseURL string `json:"releaseURL"`
}

// ApplyAgentredUpdateOptions 描述一次替换要落到哪里、怎么报进度。
type ApplyAgentredUpdateOptions struct {
	// TargetPath 要被替换的可执行文件，空串取当前可执行文件（已解析符号链接）。
	TargetPath string
	// OnProgress 可为 nil；非 nil 时按字节流回调下载进度。
	OnProgress func(downloaded, total int64)
}

// ActiveTurnsError 是「这台机器上还有对话在跑」这道闸给出的拒绝。
// 命令行与远程升级 RPC 用同一句话拒绝同一件事，两处不该长得不一样。
type ActiveTurnsError struct {
	Count int64
}

func (e *ActiveTurnsError) Error() string {
	return fmt.Sprintf("this machine has %d running conversation(s); upgrading would interrupt them", e.Count)
}

// AssetNotFoundError 这次发布里没有当前平台的 agentred 资产。
type AssetNotFoundError struct {
	Platform string
	Channel  string
}

func (e *AssetNotFoundError) Error() string {
	if e.Channel == "" {
		return fmt.Sprintf("release has no agentred asset for %s", e.Platform)
	}
	return fmt.Sprintf("the %s release has no agentred asset for %s", e.Channel, e.Platform)
}

// ChecksumMismatchError 下载到的资产与发布附带的 SHA256 对不上。
type ChecksumMismatchError struct {
	AssetName string
	Expected  string
	Actual    string
}

func (e *ChecksumMismatchError) Error() string {
	return fmt.Sprintf("SHA256 mismatch for %s (expected %s, got %s); the download was discarded and the current binary is untouched",
		e.AssetName, e.Expected, e.Actual)
}

// TargetNotWritableError 目标路径所在目录不可写。指名道姓地报出来，不改装到别处：
// 装到另一个目录会造出第二个 agentred，症状是「升级了但版本没变」。
type TargetNotWritableError struct {
	Path string
	Err  error
}

func (e *TargetNotWritableError) Error() string {
	return fmt.Sprintf("cannot replace %s: %v; re-run with enough privileges to write %s",
		e.Path, e.Err, filepath.Dir(e.Path))
}

func (e *TargetNotWritableError) Unwrap() error { return e.Err }

// NormalizeChannel 校验并归一化更新通道，空串按 stable。
func NormalizeChannel(channel string) (string, error) {
	switch strings.TrimSpace(channel) {
	case "":
		return ChannelStable, nil
	case ChannelStable:
		return ChannelStable, nil
	case ChannelBeta:
		return ChannelBeta, nil
	case ChannelNightly:
		return ChannelNightly, nil
	default:
		return "", fmt.Errorf("unknown update channel %q; expected %s, %s or %s",
			channel, ChannelStable, ChannelBeta, ChannelNightly)
	}
}

// GuardActiveTurns 是活跃轮次闸门：有轮次在跑就拒绝，越过它必须由调用方显式声明
// （命令行的 --force、RPC 请求里的显式标志）。
func GuardActiveTurns(activeTurns int64, force bool) error {
	if force || activeTurns <= 0 {
		return nil
	}
	return &ActiveTurnsError{Count: activeTurns}
}

// AgentredTargetPath 返回本次替换的目标：当前可执行文件，符号链接先解析掉，
// 否则替换掉的只是 PATH 上那个链接。
func AgentredTargetPath() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path failed: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", fmt.Errorf("resolve executable symlinks failed: %w", err)
	}
	return resolved, nil
}

// EnsureAgentredTargetWritable 在下载之前就回答「换得动吗」。替换是目录内的一次
// rename，所以真正要问的是目录可不可写。
func EnsureAgentredTargetWritable(targetPath string) error {
	dir := filepath.Dir(targetPath)
	probe, err := os.CreateTemp(dir, ".agentred-write-probe-*")
	if err != nil {
		// 只留下那个「为什么不行」，探针文件叫什么名字对用户没有意义。
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		return &TargetNotWritableError{Path: targetPath, Err: err}
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		logger.Default().Warn("close write probe", zap.String("path", name), zap.Error(err))
	}
	if err := os.Remove(name); err != nil {
		logger.Default().Warn("remove write probe", zap.String("path", name), zap.Error(err))
	}
	return nil
}

// assetNames 把 release 资产列表压成名字列表，资产选择只看名字。
func assetNames(assets []ReleaseAsset) []string {
	names := make([]string, 0, len(assets))
	for _, asset := range assets {
		names = append(names, asset.Name)
	}
	return names
}

// agentredAssetSuffix 是 make agentred-package 产出的归档后缀。
func agentredAssetSuffix(goos, goarch string) string {
	if goos == "windows" {
		return "-" + goos + "-" + goarch + ".zip"
	}
	return "-" + goos + "-" + goarch + ".tar.gz"
}

// pickAgentredAsset 从名字列表里挑出当前平台的 agentred 归档，并回报名字里带的版本。
// 前缀必须是 agentred-：桌面端的资产同样带 <os>-<arch>，只按平台子串匹配会选错东西。
func pickAgentredAsset(names []string, goos, goarch string) (assetName, version string) {
	prefix := agentredBinaryName + "-"
	suffix := agentredAssetSuffix(goos, goarch)
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		middle := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
		if middle == "" {
			continue
		}
		return name, middle
	}
	return "", ""
}

// ResolveAgentredRelease 解析目标通道的最新 agentred 发布。
//
// 三条来路按优先级：显式指定的 BaseURL（内网 / 自建源）→ GitHub → 内置镜像。
// 它只读不写，`--check` 与远程升级的受理判定都只需要走到这里。
func ResolveAgentredRelease(ctx context.Context, opts AgentredReleaseOptions) (*AgentredRelease, error) {
	channel, err := NormalizeChannel(opts.Channel)
	if err != nil {
		return nil, err
	}
	goos, goarch := opts.GOOS, opts.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	currentVersion := opts.CurrentVersion
	if currentVersion == "" {
		currentVersion = configs.Version
	}

	var release *AgentredRelease
	if strings.TrimSpace(opts.BaseURL) != "" {
		release, err = resolveAgentredFromBaseURL(ctx, strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/"), goos, goarch)
	} else {
		release, err = resolveAgentredFromGitHub(ctx, channel, goos, goarch)
	}
	if err != nil {
		return nil, err
	}

	release.Channel = channel
	release.CurrentVersion = currentVersion
	release.HasUpdate = hasUpdate(channel, currentVersion, release.LatestVersion)
	return release, nil
}

// resolveAgentredFromBaseURL 读一个「SHA256SUMS 加同目录资产」的发布源。
// 那份布局就是 scripts/install.sh 读的那份，版本号从资产名里读出来。
func resolveAgentredFromBaseURL(ctx context.Context, baseURL, goos, goarch string) (*AgentredRelease, error) {
	checksums, err := fetchChecksumsFrom(ctx, baseURL+"/SHA256SUMS")
	if err != nil {
		return nil, agentredSourceError(err)
	}
	names := make([]string, 0, len(checksums))
	for name := range checksums {
		names = append(names, name)
	}
	assetName, version := pickAgentredAsset(names, goos, goarch)
	if assetName == "" {
		return nil, &AssetNotFoundError{Platform: goos + "-" + goarch}
	}
	return &AgentredRelease{
		LatestVersion: version,
		AssetName:     assetName,
		AssetURL:      baseURL + "/" + assetName,
		SHA256:        checksums[assetName],
		ReleaseURL:    baseURL,
	}, nil
}

// resolveAgentredFromGitHub 走 GitHub API，失败后逐个试内置镜像；镜像命中时下载
// 地址也一并套上同一个前缀，否则解析走了镜像、下载还是回到连不上的 GitHub。
func resolveAgentredFromGitHub(ctx context.Context, channel, goos, goarch string) (*AgentredRelease, error) {
	release, err := fetchRelease(channel)
	mirrorPrefix := ""
	if err != nil {
		lastErr := err
		for _, mirror := range availableMirrors {
			if mirror.URL == "" {
				continue
			}
			mirrored, mirrorErr := fetchReleaseFromMirror(channel, mirror.URL)
			if mirrorErr == nil {
				release, mirrorPrefix, lastErr = mirrored, mirror.URL, nil
				break
			}
			logger.Default().Info("agentred release mirror failed",
				zap.String("mirror", mirror.ID), zap.Error(mirrorErr))
			lastErr = mirrorErr
		}
		if lastErr != nil {
			return nil, agentredSourceError(lastErr)
		}
	}

	assetName, _ := pickAgentredAsset(assetNames(release.Assets), goos, goarch)
	if assetName == "" {
		return nil, &AssetNotFoundError{Platform: goos + "-" + goarch, Channel: channel}
	}
	var assetURL string
	var checksumURL string
	for _, asset := range release.Assets {
		switch asset.Name {
		case assetName:
			assetURL = asset.BrowserDownloadURL
		case "SHA256SUMS.txt":
			checksumURL = asset.BrowserDownloadURL
		}
	}
	if checksumURL == "" {
		return nil, fmt.Errorf("release %s has no SHA256SUMS.txt", release.TagName)
	}
	checksums, err := fetchChecksumsFrom(ctx, applyMirror(checksumURL, mirrorPrefix))
	if err != nil {
		return nil, fmt.Errorf("%s%w", ChecksumFetchError, err)
	}
	sum, ok := checksums[assetName]
	if !ok {
		return nil, fmt.Errorf("SHA256SUMS.txt has no checksum for %s", assetName)
	}

	latestVersion := release.TagName
	if channel == ChannelNightly {
		latestVersion = release.Name // nightly 用 release title 作版本号
	}
	return &AgentredRelease{
		LatestVersion: latestVersion,
		AssetName:     assetName,
		AssetURL:      applyMirror(assetURL, mirrorPrefix),
		SHA256:        sum,
		ReleaseURL:    release.HTMLURL,
	}, nil
}

// agentredSourceError 把「来路不可达」翻成一句带出路的话：换源的变量名与
// scripts/install.sh 用的是同一个。
func agentredSourceError(err error) error {
	return fmt.Errorf("resolve agentred release failed: %w; %s", err, agentredSourceHint)
}

const agentredSourceHint = "point " + AgentredReleaseBaseURLEnv + " at a reachable release source"

// fetchChecksumsFrom 下载并解析一份 sha256sum 清单。
func fetchChecksumsFrom(ctx context.Context, url string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create checksum request failed: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download checksums failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close checksum response body", zap.Error(err))
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download checksums returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read checksums failed: %w", err)
	}
	return parseChecksums(string(body)), nil
}

// ApplyAgentredUpdate 下载已解析的资产、校验、替换目标可执行文件。
//
// 顺序是刻意的：先问路径换不换得动，再下载。不可写时一个字节都不下载；校验不过就
// 连临时文件一起丢掉——任何一条失败路径都不留下半个二进制。
func ApplyAgentredUpdate(ctx context.Context, release *AgentredRelease, opts ApplyAgentredUpdateOptions) error {
	if release == nil || release.AssetURL == "" {
		return errors.New("apply agentred update: release is not resolved")
	}
	targetPath := opts.TargetPath
	if targetPath == "" {
		resolved, err := AgentredTargetPath()
		if err != nil {
			return err
		}
		targetPath = resolved
	}
	if err := EnsureAgentredTargetWritable(targetPath); err != nil {
		return err
	}

	// 下载到目标所在目录：替换要用 rename，跨文件系统的 rename 会失败。
	dir := filepath.Dir(targetPath)
	archivePath, err := downloadAgentredAsset(ctx, release, dir, opts.OnProgress)
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(archivePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Default().Warn("remove downloaded archive", zap.String("path", archivePath), zap.Error(err))
		}
	}()

	extractDir, err := os.MkdirTemp(dir, ".agentred-extract-*")
	if err != nil {
		return &TargetNotWritableError{Path: targetPath, Err: err}
	}
	defer func() {
		if err := os.RemoveAll(extractDir); err != nil {
			logger.Default().Warn("remove extract dir", zap.String("path", extractDir), zap.Error(err))
		}
	}()

	if strings.HasSuffix(release.AssetName, ".zip") {
		err = extractZip(archivePath, extractDir)
	} else {
		err = extractTarGz(archivePath, extractDir)
	}
	if err != nil {
		return fmt.Errorf("extract %s failed: %w", release.AssetName, err)
	}

	newBinary := filepath.Join(extractDir, agentredBinaryName)
	if strings.HasSuffix(release.AssetName, ".zip") {
		newBinary += ".exe"
	}
	if _, err := os.Stat(newBinary); err != nil {
		return fmt.Errorf("%s does not contain %s: %w", release.AssetName, filepath.Base(newBinary), err)
	}
	return installAgentredBinary(newBinary, targetPath)
}

// downloadAgentredAsset 边下边算 SHA256，对不上就把临时文件一起丢掉。
func downloadAgentredAsset(ctx context.Context, release *AgentredRelease, dir string,
	onProgress func(downloaded, total int64)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, release.AssetURL, nil)
	if err != nil {
		return "", fmt.Errorf("create download request failed: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s failed: %w; %s", release.AssetName, err, agentredSourceHint)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close download response body", zap.Error(err))
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s returned status %d", release.AssetName, resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp(dir, ".agentred-download-*")
	if err != nil {
		return "", &TargetNotWritableError{Path: filepath.Join(dir, agentredBinaryName), Err: err}
	}
	tmpPath := tmpFile.Name()
	discard := func() {
		if err := os.Remove(tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Default().Warn("remove partial download", zap.String("path", tmpPath), zap.Error(err))
		}
	}

	hasher := sha256.New()
	reader := io.TeeReader(resp.Body, hasher)
	if onProgress != nil {
		reader = &progressReader{r: reader, total: resp.ContentLength, onProgress: onProgress}
	}
	if _, err := io.Copy(tmpFile, reader); err != nil {
		if closeErr := tmpFile.Close(); closeErr != nil {
			logger.Default().Warn("close partial download", zap.Error(closeErr))
		}
		discard()
		return "", fmt.Errorf("download %s failed: %w", release.AssetName, err)
	}
	if err := tmpFile.Close(); err != nil {
		discard()
		return "", fmt.Errorf("close download failed: %w", err)
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	if release.SHA256 == "" {
		discard()
		return "", fmt.Errorf("release has no SHA256 for %s", release.AssetName)
	}
	if !strings.EqualFold(actual, release.SHA256) {
		discard()
		return "", &ChecksumMismatchError{AssetName: release.AssetName, Expected: release.SHA256, Actual: actual}
	}
	return tmpPath, nil
}

// installAgentredBinary 原子替换：同目录里先落成型，再一次 rename 换过去。
// rename 失败（Windows 覆盖不了正在运行的 exe）时把旧的挪开重试，失败则挪回来。
func installAgentredBinary(newBinary, targetPath string) error {
	staged := filepath.Join(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".new")
	if err := copyFile(newBinary, staged, 0o755); err != nil {
		if removeErr := os.Remove(staged); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			logger.Default().Warn("remove staged binary", zap.String("path", staged), zap.Error(removeErr))
		}
		return fmt.Errorf("stage new binary failed: %w", err)
	}
	// copyFile 只在创建时用 perm，命中残留文件时权限位仍是旧的，这里补齐。
	if err := os.Chmod(staged, 0o755); err != nil {
		return fmt.Errorf("mark new binary executable failed: %w", err)
	}

	if err := os.Rename(staged, targetPath); err == nil {
		return nil
	}

	backup := targetPath + ".old"
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Default().Warn("remove previous backup", zap.String("path", backup), zap.Error(err))
	}
	if err := os.Rename(targetPath, backup); err != nil {
		return fmt.Errorf("move current binary aside failed: %w", err)
	}
	if err := os.Rename(staged, targetPath); err != nil {
		if restoreErr := os.Rename(backup, targetPath); restoreErr != nil {
			logger.Default().Error("restore binary after failed install",
				zap.String("backup", backup), zap.Error(restoreErr))
		}
		return fmt.Errorf("install new binary failed: %w", err)
	}
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Windows 删不掉正在运行的 exe，留着不影响新二进制生效。
		logger.Default().Warn("remove replaced binary", zap.String("path", backup), zap.Error(err))
	}
	return nil
}
