// Package update_svc 提供桌面端"检查更新 / 下载校验 / 跨平台安装"能力。
//
// 设计要点：
//   - 通过 GitHub Releases API 获取版本信息，支持 stable/beta/nightly 三通道
//   - 国内访问失败时可走镜像（ghfast.top / gh-proxy.com 等），见 mirror.go
//   - 下载边写边算 SHA256，与 release 中的 SHA256SUMS.txt 比对防篡改
//   - macOS .app / Linux 二进制 / Windows NSIS installer 三套安装路径，失败自动回滚
package update_svc

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

const (
	// githubRepo agentre 的 GitHub 仓库路径（owner/name）。
	// 若仓库迁移需同步修改 release.yml 里的 gh api 路径。
	githubRepo = "agentre-hub/agentre"
	apiBaseURL = "https://api.github.com/repos/" + githubRepo
	// githubDownloadBaseURL 是发布下载的权威域名前缀。
	githubDownloadBaseURL = "https://github.com/" + githubRepo

	// ChannelStable 稳定版更新通道
	ChannelStable = "stable"
	// ChannelBeta 测试版更新通道
	ChannelBeta = "beta"
	// ChannelNightly 每日构建更新通道
	ChannelNightly = "nightly"

	// ChecksumFetchError 校验文件获取失败的错误前缀，agentred 侧用它标出这一类失败。
	// 桌面端不再带它：那个前缀原先只为让前端弹「跳过校验继续」，出口删掉后它在界面
	// 上只是一段机器噪声。
	ChecksumFetchError = "CHECKSUM_FETCH_FAILED:"
)

var errNoStableRelease = errors.New("no stable release found")

// errNoBetaRelease 是桌面端 Beta 通道找不到 vX.Y.Z-beta.N 非 draft 发布时的哨兵：
// 决定 5 要求没有这样的发布就是「暂无更新」，不回落到正式版（agentred 共用的
// fetchLatestBetaRelease 仍保留回落，两者刻意分开，见 pickLatestDesktopBetaRelease）。
var errNoBetaRelease = errors.New("no beta release found for desktop channel")

// errDevNoRelease 是 Dev 渠道的哨兵：决定 9「Dev 没有发布」，CheckForUpdate /
// DownloadAndUpdate 对 Dev 一律不选中任何 release 或安装包。
var errDevNoRelease = errors.New("dev channel has no release to update to")

// ReleaseAsset GitHub release 资产
type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// ReleaseInfo GitHub release 信息
type ReleaseInfo struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt string         `json:"published_at"`
	Prerelease  bool           `json:"prerelease"`
	Draft       bool           `json:"draft"`
	Assets      []ReleaseAsset `json:"assets"`
}

// UpdateInfo 更新检查结果
type UpdateInfo struct {
	HasUpdate      bool   `json:"hasUpdate"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	ReleaseNotes   string `json:"releaseNotes"`
	ReleaseURL     string `json:"releaseURL"`
	PublishedAt    string `json:"publishedAt"`
}

// fetchRelease 根据通道获取对应的 release 信息
func fetchRelease(channel string) (*ReleaseInfo, error) {
	switch channel {
	case ChannelNightly:
		return fetchReleaseFromURL(apiBaseURL + "/releases/tags/nightly")
	case ChannelBeta:
		return fetchLatestBetaRelease()
	default:
		return fetchLatestStableRelease()
	}
}

// fetchReleaseFromURL 从指定 URL 获取单个 release
func fetchReleaseFromURL(url string) (*ReleaseInfo, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request GitHub API failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}
	return &release, nil
}

func fetchReleasesFromURL(url string) ([]ReleaseInfo, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create releases request failed: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request GitHub releases API failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close response body", zap.Error(err))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub releases API returned status %d", resp.StatusCode)
	}

	var releases []ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode releases response failed: %w", err)
	}
	return releases, nil
}

// fetchLatestStableRelease 获取最新正式版 release。GitHub /releases/latest 在仓库
// 只有 prerelease/nightly 时会返回 404,所以这里列出 releases 后本地筛选。
func fetchLatestStableRelease() (*ReleaseInfo, error) {
	releases, err := fetchReleasesFromURL(apiBaseURL + "/releases?per_page=20")
	if err != nil {
		return nil, err
	}
	return pickLatestStableRelease(releases)
}

func pickLatestStableRelease(releases []ReleaseInfo) (*ReleaseInfo, error) {
	for i := range releases {
		if releases[i].Draft || releases[i].Prerelease || releases[i].TagName == "nightly" {
			continue
		}
		return &releases[i], nil
	}
	return nil, errNoStableRelease
}

// fetchLatestBetaRelease 获取最新的 beta 或 stable release（排除 nightly）
func fetchLatestBetaRelease() (*ReleaseInfo, error) {
	releases, err := fetchReleasesFromURL(apiBaseURL + "/releases?per_page=20")
	if err != nil {
		return nil, err
	}
	for i := range releases {
		if !releases[i].Draft && releases[i].TagName != "nightly" {
			return &releases[i], nil
		}
	}
	return nil, fmt.Errorf("no beta or stable release found")
}

// betaTagPattern 是桌面端 Beta 通道接受的发布号形状：vX.Y.Z-beta.N。决定 6 已经
// 不再使用 -rc tag，这里只认这一种预发布形状。
var betaTagPattern = regexp.MustCompile(`^v\d+\.\d+\.\d+-beta\.\d+$`)

// fetchLatestDesktopBetaRelease 取桌面端 Beta 通道的最新发布：只认 vX.Y.Z-beta.N 的
// 非 draft 预发布，没有就是 errNoBetaRelease —— 与 agentred 共用的
// fetchLatestBetaRelease（保留正式版回落）刻意分成两条路，见该函数注释。
func fetchLatestDesktopBetaRelease() (*ReleaseInfo, error) {
	releases, err := fetchReleasesFromURL(apiBaseURL + "/releases?per_page=20")
	if err != nil {
		return nil, err
	}
	return pickLatestDesktopBetaRelease(releases)
}

// pickLatestDesktopBetaRelease 是 fetchLatestDesktopBetaRelease 的纯逻辑部分，
// 抽出来是为了在不打网络请求的情况下单测「只认 vX.Y.Z-beta.N 的预发布、跳过 draft、不回落」。
// GitHub releases 列表按创建时间倒序，第一个匹配的就是最新的。
func pickLatestDesktopBetaRelease(releases []ReleaseInfo) (*ReleaseInfo, error) {
	for i := range releases {
		if releases[i].Draft || !releases[i].Prerelease {
			continue
		}
		if betaTagPattern.MatchString(releases[i].TagName) {
			return &releases[i], nil
		}
	}
	return nil, errNoBetaRelease
}

// fetchReleaseForChannel 是桌面端按渠道选 release 的入口：与 agentred 共用的
// fetchRelease 不同，Beta 走严格匹配不回落，Dev 直接拒绝、不发任何请求。
func fetchReleaseForChannel(channel paths.Channel) (*ReleaseInfo, error) {
	switch channel {
	case paths.ChannelDev:
		return nil, errDevNoRelease
	case paths.ChannelNightly:
		return fetchReleaseFromURL(apiBaseURL + "/releases/tags/nightly")
	case paths.ChannelBeta:
		return fetchLatestDesktopBetaRelease()
	default:
		return fetchLatestStableRelease()
	}
}

// releaseInfoURL 返回指定通道的 release-info.json 下载地址
// beta 通道无固定地址，返回空字符串
func releaseInfoURL(channel string) string {
	switch channel {
	case ChannelStable:
		return githubDownloadBaseURL + "/releases/latest/download/release-info.json"
	case ChannelNightly:
		return githubDownloadBaseURL + "/releases/download/nightly/release-info.json"
	default:
		return ""
	}
}

// releaseTagPattern 是发布号的形状。tag 可能来自镜像供的元数据，只有形状对得上
// 才拿去拼权威地址。
var releaseTagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// authoritativeChecksumURL 按 tag 给出 SHA256SUMS.txt 的权威地址。
//
// 地址只从常量与 tag 拼出来，不读 release 元数据里带的那个 URL：元数据可能来自
// 镜像，那 URL 就由镜像说了算，于是校验和与被它校验的二进制同源 —— 校验只能证明
// 「镜像自洽」，代理运营方单方面即可投毒。
func authoritativeChecksumURL(baseURL, tag string) (string, error) {
	if !releaseTagPattern.MatchString(tag) {
		return "", fmt.Errorf("发布号 %q 不是可用的版本号，无法定位官方校验文件", tag)
	}
	return baseURL + "/releases/download/" + tag + "/SHA256SUMS.txt", nil
}

// fetchReleaseFromMirror 通过镜像下载 release-info.json 获取 release 信息
func fetchReleaseFromMirror(channel, mirrorPrefix string) (*ReleaseInfo, error) {
	infoURL := releaseInfoURL(channel)
	if infoURL == "" {
		return nil, fmt.Errorf("channel %s does not support mirror fallback", channel)
	}

	mirroredURL := applyMirror(infoURL, mirrorPrefix)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(mirroredURL) //nolint:noctx // mirror URL constructed from constants
	if err != nil {
		return nil, fmt.Errorf("request mirror failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Default().Warn("close mirror response body", zap.Error(err))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mirror returned status %d", resp.StatusCode)
	}

	var release ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode mirror response failed: %w", err)
	}
	return &release, nil
}

// releaseSources 是解析一次发布要用到的几条来路。分开列出来是因为它们的信任级别
// 不同：fetchRelease 与 checksumBaseURL 是权威来源，决定「装什么」与「校验值是
// 多少」；mirrors 只是大文件的代下载，它说的校验值一律不采纳。
type releaseSources struct {
	// fetchRelease 取权威发布元数据（api.github.com）。
	fetchRelease func(channel string) (*ReleaseInfo, error)
	// mirrors 内置镜像列表，用于资产下载（以及 agentred 侧元数据的最后回落）。
	mirrors []MirrorInfo
	// checksumBaseURL 校验和清单的权威来源前缀，永不套镜像前缀。
	checksumBaseURL string
}

// defaultReleaseSources 是生产来路，供 agentred 复用（agentred.go:241,283）——它的
// fetchRelease 字段解析 Beta 时保留回落到最新非 nightly 发布的旧行为，decision 11
// 要求 agentred 不按渠道拆分。
func defaultReleaseSources() releaseSources {
	return releaseSources{
		fetchRelease:    fetchRelease,
		mirrors:         availableMirrors,
		checksumBaseURL: githubDownloadBaseURL,
	}
}

// defaultDesktopReleaseSources 是桌面端安装路径（downloadAndUpdateForChannel）的
// 生产来路：fetchRelease 换成 fetchReleaseForChannel，Beta 严格匹配不回落、Dev 直接
// 拒绝，与 agentred 共用的 defaultReleaseSources 刻意分开。
func defaultDesktopReleaseSources() releaseSources {
	return releaseSources{
		fetchRelease: func(channel string) (*ReleaseInfo, error) {
			return fetchReleaseForChannel(paths.Channel(channel))
		},
		checksumBaseURL: githubDownloadBaseURL,
	}
}

// checkForUpdateForChannel 检查指定渠道的最新版本。
func checkForUpdateForChannel(channel paths.Channel, mirrorPrefix string) (*UpdateInfo, error) {
	return checkForUpdateForChannelWithFetch(channel, mirrorPrefix, fetchReleaseForChannel)
}

// checkForUpdateForChannelWithFetch 是 checkForUpdateForChannel 的可注入版本：fetch
// 参数化是为了让「Beta 暂无更新 / Dev 不发请求 / 镜像回落」这几条分支能脱离真实网络
// 单测，而不用像 fetchRelease 那样只能靠间接的纯函数（pickLatestStableRelease 等）
// 覆盖。
func checkForUpdateForChannelWithFetch(channel paths.Channel, mirrorPrefix string,
	fetch func(paths.Channel) (*ReleaseInfo, error)) (*UpdateInfo, error) {
	// Dev 没有发布（决定 9）：连一次请求都不发，不靠 fetch 内部的哨兵兜底。
	if channel == paths.ChannelDev {
		return nil, errDevNoRelease
	}

	release, err := fetch(channel)
	if errors.Is(err, errNoStableRelease) || errors.Is(err, errNoBetaRelease) {
		return &UpdateInfo{
			HasUpdate:      false,
			CurrentVersion: configs.Version,
		}, nil
	}
	if err != nil && mirrorPrefix != "" {
		logger.Default().Info("GitHub API failed, trying mirror fallback",
			zap.String("channel", string(channel)), zap.Error(err))
		release, err = fetchReleaseFromMirror(string(channel), mirrorPrefix)
	}
	if err != nil {
		return nil, err
	}

	currentVersion := configs.Version
	latestVersion := release.TagName
	if channel == paths.ChannelNightly {
		latestVersion = release.Name // nightly 用 release title 作为版本号
	}

	info := &UpdateInfo{
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
		ReleaseNotes:   release.Body,
		ReleaseURL:     release.HTMLURL,
		PublishedAt:    release.PublishedAt,
	}

	info.HasUpdate = hasUpdate(string(channel), currentVersion, latestVersion)
	return info, nil
}

// isNightlyVersion 判断是否为 nightly 版本
func isNightlyVersion(version string) bool {
	return strings.Contains(version, "nightly.") || strings.HasPrefix(version, "nightly-")
}

// hasUpdate 判断是否有更新
func hasUpdate(channel, currentVersion, latestVersion string) bool {
	if currentVersion == "dev" || currentVersion == "" {
		return true
	}

	isCurrentNightly := isNightlyVersion(currentVersion)

	if channel == ChannelNightly {
		if !isCurrentNightly {
			return true // 从 stable/beta 切换到 nightly
		}
		// 旧格式 nightly-YYYYMMDD-SHA 直接字符串比较
		if strings.HasPrefix(currentVersion, "nightly-") {
			return currentVersion != latestVersion
		}
		// 新格式使用语义化版本比较
		cv := strings.TrimPrefix(currentVersion, "v")
		lv := strings.TrimPrefix(latestVersion, "v")
		return compareVersions(lv, cv) > 0
	}

	// stable 或 beta 通道
	if isCurrentNightly {
		return true // 从 nightly 切换到 stable/beta
	}

	cv := strings.TrimPrefix(currentVersion, "v")
	lv := strings.TrimPrefix(latestVersion, "v")
	return compareVersions(lv, cv) > 0
}

// resolveInstallRelease 取「决定装什么」的那份发布元数据。
//
// 它只认权威来源，签名上就不收镜像前缀：这份元数据同时决定资产地址与发布号，
// 而发布号又决定去哪儿取校验和 —— 让镜像来供它，校验和与被它校验的二进制就同源，
// 校验只能证明「镜像自洽」。取不到就失败，不降级（镜像仍然给资产下载用）。
func resolveInstallRelease(sources releaseSources, channel string) (*ReleaseInfo, error) {
	release, err := sources.fetchRelease(channel)
	if errors.Is(err, errNoStableRelease) || errors.Is(err, errNoBetaRelease) || errors.Is(err, errDevNoRelease) {
		return nil, err
	}
	if err != nil {
		logger.Default().Info("resolve release metadata failed",
			zap.String("channel", channel), zap.Error(err))
		return nil, fmt.Errorf("获取 GitHub 官方版本信息失败: %w；"+
			"版本信息与校验文件只从 GitHub 官方地址获取，不走下载镜像，请确认能访问 github.com 后重试", err)
	}
	return release, nil
}

// installChecksums 取这次安装要比对的校验和清单，只从权威域名按发布号取。
//
// 取不到是错误而不是「跳过校验」：调用方拿到 nil 校验表就不校验，于是一次取不到
// 校验文件的安装会被无声地放行。取不到校验和就装不上，没有跳过它的出口 —— 有出口
// 的话，社工一句「点跳过就能装」即可绕掉整条防线。
func installChecksums(sources releaseSources, release *ReleaseInfo) (map[string]string, error) {
	url, err := authoritativeChecksumURL(sources.checksumBaseURL, release.TagName)
	if err != nil {
		return nil, err
	}
	return fetchChecksumsFrom(context.Background(), url)
}

// desktopAssetShape 是桌面安装包名字里「前缀之后」那一段该有的形状：一个版本号
// (v?数字打头，允许任意 -beta.N / -nightly.YYYYMMDD 之类的预发布后缀)，紧接
// "-<goos>-<goarch>"，Windows NSIS 安装器另带 "-installer"，再跟扩展名。要求版本段以数字（或 v+数字）开头，是为了让
// "agentre-" 这个较短前缀在面对 "agentre-beta-…" / "agentre-nightly-…" 这类较长
// 前缀的资产名时匹配失败 —— 去掉 "agentre-" 后剩下的是 "beta-…"/"nightly-…"，
// 不是版本号形状，从而不会被 stable 错选中。
func desktopAssetShape(goos, goarch string) *regexp.Regexp {
	return regexp.MustCompile(`^v?[0-9].*-` + regexp.QuoteMeta(goos) + `-` + regexp.QuoteMeta(goarch) + `(-installer)?\.[0-9A-Za-z.]+$`)
}

// pickDesktopAsset 从一次发布的资产列表里选出本渠道、本平台的桌面安装包：名字必须
// 以 prefix 开头，紧跟 desktopAssetShape。prefix 为空（Dev 不发布）时永远选不中
// 任何东西。agentred 归档的前缀是 "agentred-"，不是任何桌面渠道前缀的合法延伸，
// 天然被排除；较长的渠道前缀（agentre-beta-/agentre-nightly-）也不会被较短的
// "agentre-" 误选，见 desktopAssetShape 的注释。没有匹配时返回的错误就是要展示
// 给用户的原文，不需要调用方再包一层。
func pickDesktopAsset(assets []ReleaseAsset, prefix, goos, goarch string) (*ReleaseAsset, error) {
	if prefix != "" {
		shape := desktopAssetShape(goos, goarch)
		for i := range assets {
			if rest, ok := strings.CutPrefix(assets[i].Name, prefix); ok && shape.MatchString(rest) {
				return &assets[i], nil
			}
		}
	}
	return nil, errors.New("该版本没有适用于本平台的安装包")
}

// desktopBundleFileName 是要从 macOS 安装包里取出的 .app 目录名：渠道显示名 + ".app"
// （例如 Beta 取 "Agentre Beta.app"），与渠道身份表一致。抽成纯函数是为了不用跑
// hdiutil / 解 tar.gz 就能单测这条渠道 → 文件名的映射。
func desktopBundleFileName(channel paths.Channel) string {
	return channel.Identity().DisplayName + ".app"
}

// downloadAndUpdateForChannel 下载指定渠道的最新版本并替换当前二进制。
//
// 校验是无条件的：取不到权威校验和就不装，没有「跳过校验继续」的入参。
func downloadAndUpdateForChannel(channel paths.Channel, mirrorPrefix string, onProgress func(downloaded, total int64)) error {
	// Dev 没有发布（决定 9）：不选中任何 release，更不会走到选包这一步。
	if channel == paths.ChannelDev {
		return errDevNoRelease
	}

	sources := defaultDesktopReleaseSources()
	release, err := resolveInstallRelease(sources, string(channel))
	if err != nil {
		return err
	}

	// 找到本渠道、本平台的桌面端安装包：只接受 <本渠道前缀><版本号>-<goos>-<goarch>，
	// agentred- 归档与其他渠道的包永远不会被选中（见 pickDesktopAsset）。
	asset, err := pickDesktopAsset(release.Assets, channel.Identity().ReleaseAssetPrefix, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	downloadURL := asset.BrowserDownloadURL
	assetSize := asset.Size
	assetName := asset.Name

	// 获取校验信息。取不到就在这里失败，说清这一次为什么没有镜像出路、以及还能
	// 怎么装上，而不是给一个「跳过校验继续」的按钮。
	checksums, err := installChecksums(sources, release)
	if err != nil {
		return fmt.Errorf("获取官方校验文件失败: %w；校验文件只从 github.com 官方地址获取，"+
			"绝不经下载镜像，请确认能访问 github.com 后重试，或前往 %s 手动下载",
			err, release.HTMLURL)
	}

	// 下载资产
	actualDownloadURL := applyMirror(downloadURL, mirrorPrefix)
	dlClient := &http.Client{Timeout: 30 * time.Minute}
	dlResp, err := dlClient.Get(actualDownloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer func() {
		if err := dlResp.Body.Close(); err != nil {
			logger.Default().Warn("close download response body", zap.Error(err))
		}
	}()

	if dlResp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", dlResp.StatusCode)
	}

	if assetSize == 0 {
		assetSize = dlResp.ContentLength
	}

	// 下载到临时文件（保留扩展名以便后续判断格式）
	ext := filepath.Ext(assetName)
	tmpFile, err := os.CreateTemp("", "agentre-update-*"+ext)
	if err != nil {
		return fmt.Errorf("create temp file failed: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		if err := os.Remove(tmpPath); err != nil {
			logger.Default().Warn("remove temp file", zap.String("path", tmpPath), zap.Error(err))
		}
	}()

	// 边下载边计算 SHA256
	hasher := sha256.New()
	reader := io.TeeReader(dlResp.Body, hasher)
	if onProgress != nil {
		reader = &progressReader{r: reader, total: assetSize, onProgress: onProgress}
	}

	if _, err := io.Copy(tmpFile, reader); err != nil {
		if closeErr := tmpFile.Close(); closeErr != nil {
			logger.Default().Warn("close temp file after write error", zap.Error(closeErr))
		}
		return fmt.Errorf("download write failed: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		logger.Default().Warn("close temp file", zap.Error(err))
	}

	// 校验 SHA256。清单一定在手上（取不到已经在上面失败），所以这里不再有
	// 「没有清单就跳过比对」的分支。
	actualHash := hex.EncodeToString(hasher.Sum(nil))
	expectedHash, ok := checksums[assetName]
	if !ok {
		return fmt.Errorf("SHA256SUMS.txt 中未找到 %s 的校验值，请前往 %s 手动下载", assetName, release.HTMLURL)
	}
	if !strings.EqualFold(actualHash, expectedHash) {
		return fmt.Errorf("文件校验失败: %s 的 SHA256 不匹配 (期望: %s, 实际: %s)，文件可能已损坏或被篡改，请前往 %s 手动下载",
			assetName, expectedHash, actualHash, release.HTMLURL)
	}

	// 获取当前可执行文件路径
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path failed: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve executable path failed: %w", err)
	}

	// 解压并替换
	switch runtime.GOOS {
	case "darwin":
		return updateMacOS(tmpPath, execPath, channel)
	case "windows":
		return updateWindows(tmpPath, execPath)
	default:
		return updateLinux(tmpPath, execPath, channel)
	}
}

// updateMacOS 更新 macOS .app bundle
func updateMacOS(archivePath, execPath string, channel paths.Channel) error {
	// execPath 类似 /path/to/Agentre.app/Contents/MacOS/Agentre
	// 需要找到 .app 目录
	appDir := execPath
	for !strings.HasSuffix(appDir, ".app") && appDir != "/" {
		appDir = filepath.Dir(appDir)
	}
	if !strings.HasSuffix(appDir, ".app") {
		// 非 .app bundle，按 Linux 方式处理
		return updateLinux(archivePath, execPath, channel)
	}

	bundleName := desktopBundleFileName(channel)
	if strings.HasSuffix(archivePath, ".dmg") {
		return updateMacOSFromDMG(archivePath, appDir, bundleName)
	}
	return updateMacOSFromTarGz(archivePath, appDir, bundleName)
}

// updateMacOSFromDMG 从 DMG 文件更新 macOS .app bundle
func updateMacOSFromDMG(dmgPath, appDir, bundleName string) error {
	mountPoint, err := os.MkdirTemp("", "agentre-mount-*")
	if err != nil {
		return fmt.Errorf("create mount point failed: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(mountPoint); err != nil {
			logger.Default().Warn("remove mount point", zap.String("path", mountPoint), zap.Error(err))
		}
	}()

	// 挂载 DMG
	if output, err := exec.Command("hdiutil", "attach", dmgPath, "-mountpoint", mountPoint, "-nobrowse", "-quiet").CombinedOutput(); err != nil { //nolint:gosec
		return fmt.Errorf("mount DMG failed: %s: %w", string(output), err)
	}
	defer func() {
		if output, err := exec.Command("hdiutil", "detach", mountPoint, "-quiet").CombinedOutput(); err != nil { //nolint:gosec
			logger.Default().Warn("unmount DMG", zap.String("output", string(output)), zap.Error(err))
		}
	}()

	newAppPath := filepath.Join(mountPoint, bundleName)
	if _, err := os.Stat(newAppPath); err != nil {
		return fmt.Errorf("app not found in DMG: %w", err)
	}

	// 备份旧的 .app
	backupDir := appDir + ".backup"
	if err := os.RemoveAll(backupDir); err != nil {
		logger.Default().Warn("remove old backup dir", zap.String("path", backupDir), zap.Error(err))
	}
	if err := os.Rename(appDir, backupDir); err != nil {
		return fmt.Errorf("backup old app failed: %w", err)
	}

	// 从挂载点复制新的 .app（跨挂载点无法 rename）
	if output, err := exec.Command("cp", "-R", newAppPath, appDir).CombinedOutput(); err != nil { //nolint:gosec
		if renameErr := os.Rename(backupDir, appDir); renameErr != nil {
			logger.Default().Error("restore backup after failed install", zap.Error(renameErr))
		}
		return fmt.Errorf("install new app failed: %s: %w", string(output), err)
	}

	if err := os.RemoveAll(backupDir); err != nil {
		logger.Default().Warn("remove backup dir", zap.String("path", backupDir), zap.Error(err))
	}
	return nil
}

// updateMacOSFromTarGz 从 tar.gz 更新 macOS .app bundle
func updateMacOSFromTarGz(archivePath, appDir, bundleName string) error {
	tmpExtractDir, err := os.MkdirTemp("", "agentre-extract-*")
	if err != nil {
		return fmt.Errorf("create temp dir failed: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpExtractDir); err != nil {
			logger.Default().Warn("remove temp extract dir", zap.String("path", tmpExtractDir), zap.Error(err))
		}
	}()

	if err := extractTarGz(archivePath, tmpExtractDir); err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}

	newAppDir := filepath.Join(tmpExtractDir, bundleName)
	if _, err := os.Stat(newAppDir); err != nil {
		return fmt.Errorf("extracted app not found: %w", err)
	}

	backupDir := appDir + ".backup"
	if err := os.RemoveAll(backupDir); err != nil {
		logger.Default().Warn("remove old backup dir", zap.String("path", backupDir), zap.Error(err))
	}
	if err := os.Rename(appDir, backupDir); err != nil {
		return fmt.Errorf("backup old app failed: %w", err)
	}

	if err := os.Rename(newAppDir, appDir); err != nil {
		if renameErr := os.Rename(backupDir, appDir); renameErr != nil {
			logger.Default().Error("restore backup after failed install", zap.Error(renameErr))
		}
		return fmt.Errorf("install new app failed: %w", err)
	}

	if err := os.RemoveAll(backupDir); err != nil {
		logger.Default().Warn("remove backup dir", zap.String("path", backupDir), zap.Error(err))
	}
	return nil
}

// updateLinux 更新 Linux 二进制，取本渠道的命令名（渠道身份表 Linux 行）。
func updateLinux(archivePath, execPath string, channel paths.Channel) error {
	command := channel.Identity().LinuxCommand
	if strings.HasSuffix(archivePath, ".deb") {
		return updateLinuxFromDeb(archivePath, execPath, command)
	}
	return updateLinuxFromTarGz(archivePath, execPath, command)
}

// updateLinuxFromDeb 从 deb 包提取二进制并替换
func updateLinuxFromDeb(debPath, execPath, command string) error {
	tmpExtractDir, err := os.MkdirTemp("", "agentre-extract-*")
	if err != nil {
		return fmt.Errorf("create temp dir failed: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpExtractDir); err != nil {
			logger.Default().Warn("remove temp extract dir", zap.String("path", tmpExtractDir), zap.Error(err))
		}
	}()

	if output, err := exec.Command("dpkg", "-x", debPath, tmpExtractDir).CombinedOutput(); err != nil { //nolint:gosec
		return fmt.Errorf("extract deb failed: %s: %w", string(output), err)
	}

	newBin := filepath.Join(tmpExtractDir, "usr", "bin", command)
	if _, err := os.Stat(newBin); err != nil {
		return fmt.Errorf("extracted binary not found: %w", err)
	}

	return replaceBinary(newBin, execPath)
}

// updateLinuxFromTarGz 从 tar.gz 提取二进制并替换
func updateLinuxFromTarGz(archivePath, execPath, command string) error {
	tmpExtractDir, err := os.MkdirTemp("", "agentre-extract-*")
	if err != nil {
		return fmt.Errorf("create temp dir failed: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpExtractDir); err != nil {
			logger.Default().Warn("remove temp extract dir", zap.String("path", tmpExtractDir), zap.Error(err))
		}
	}()

	if err := extractTarGz(archivePath, tmpExtractDir); err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}

	newBin := filepath.Join(tmpExtractDir, command)
	if _, err := os.Stat(newBin); err != nil {
		return fmt.Errorf("extracted binary not found: %w", err)
	}

	return replaceBinary(newBin, execPath)
}

// replaceBinary 备份旧二进制并替换为新二进制
func replaceBinary(newBin, execPath string) error {
	backupPath := execPath + ".backup"
	if err := os.Remove(backupPath); err != nil {
		logger.Default().Warn("remove old backup", zap.String("path", backupPath), zap.Error(err))
	}
	if err := os.Rename(execPath, backupPath); err != nil {
		return fmt.Errorf("backup old binary failed: %w", err)
	}

	if err := copyFile(newBin, execPath, 0755); err != nil {
		if renameErr := os.Rename(backupPath, execPath); renameErr != nil {
			logger.Default().Error("restore backup after failed install", zap.Error(renameErr))
		}
		return fmt.Errorf("install new binary failed: %w", err)
	}

	if err := os.Remove(backupPath); err != nil {
		logger.Default().Warn("remove backup", zap.String("path", backupPath), zap.Error(err))
	}
	return nil
}

// updateWindows 更新 Windows 二进制
func updateWindows(archivePath, execPath string) error {
	if strings.HasSuffix(archivePath, ".exe") {
		return updateWindowsFromInstaller(archivePath, execPath)
	}
	return updateWindowsFromZip(archivePath, execPath)
}

// updateWindowsFromInstaller 运行 NSIS 安装程序静默更新（用户级安装，无需 UAC）
// Windows 不能覆盖正在运行的 exe，需要先 rename 再运行安装程序
func updateWindowsFromInstaller(installerPath, execPath string) error {
	installDir := filepath.Dir(execPath)

	// Windows 允许 rename 正在运行的 exe，但不允许覆盖
	backupPath := execPath + ".old"
	if err := os.Remove(backupPath); err != nil {
		logger.Default().Warn("remove old backup", zap.String("path", backupPath), zap.Error(err))
	}
	if err := os.Rename(execPath, backupPath); err != nil {
		return fmt.Errorf("backup running binary failed: %w", err)
	}

	// 运行 NSIS 安装程序，/D= 必须是最后一个参数且指定安装目录
	if err := runInstaller(installerPath, "/S", "/D="+installDir); err != nil {
		if renameErr := os.Rename(backupPath, execPath); renameErr != nil {
			logger.Default().Error("restore backup after failed install", zap.Error(renameErr))
		}
		return err
	}

	// 验证新 exe 是否已安装到位
	if _, err := os.Stat(execPath); err != nil {
		if renameErr := os.Rename(backupPath, execPath); renameErr != nil {
			logger.Default().Error("restore backup after missing binary", zap.Error(renameErr))
		}
		return fmt.Errorf("installer did not produce binary at %s", execPath)
	}

	return nil
}

// updateWindowsFromZip 从 zip 提取二进制并替换
func updateWindowsFromZip(archivePath, execPath string) error {
	tmpExtractDir, err := os.MkdirTemp("", "agentre-extract-*")
	if err != nil {
		return fmt.Errorf("create temp dir failed: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpExtractDir); err != nil {
			logger.Default().Warn("remove temp extract dir", zap.String("path", tmpExtractDir), zap.Error(err))
		}
	}()

	if err := extractZip(archivePath, tmpExtractDir); err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}

	newBin := filepath.Join(tmpExtractDir, "agentre.exe")
	if _, err := os.Stat(newBin); err != nil {
		return fmt.Errorf("extracted binary not found: %w", err)
	}

	// Windows 不能替换正在运行的 exe，重命名旧文件后复制新文件
	backupPath := execPath + ".old"
	if err := os.Remove(backupPath); err != nil {
		logger.Default().Warn("remove old backup", zap.String("path", backupPath), zap.Error(err))
	}
	if err := os.Rename(execPath, backupPath); err != nil {
		return fmt.Errorf("backup old binary failed: %w", err)
	}

	if err := copyFile(newBin, execPath, 0755); err != nil {
		if renameErr := os.Rename(backupPath, execPath); renameErr != nil {
			logger.Default().Error("restore backup after failed install", zap.Error(renameErr))
		}
		return fmt.Errorf("install new binary failed: %w", err)
	}

	// 旧的 .old 文件留着，下次启动时可以清理
	return nil
}

// extractTarGz 解压 tar.gz 到指定目录
func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath) //nolint:gosec // extracting trusted archive
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			logger.Default().Warn("close archive file", zap.Error(err))
		}
	}()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() {
		if err := gz.Close(); err != nil {
			logger.Default().Warn("close gzip reader", zap.Error(err))
		}
	}()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// 安全检查: 防止路径遍历
		target := filepath.Join(destDir, header.Name) //nolint:gosec // extracting trusted archive
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)) //nolint:gosec // extracting trusted archive
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tr); err != nil { //nolint:gosec // trusted archive source
				if closeErr := outFile.Close(); closeErr != nil {
					logger.Default().Warn("close extracted file after copy error", zap.Error(closeErr))
				}
				return err
			}
			if err := outFile.Close(); err != nil {
				logger.Default().Warn("close extracted file", zap.Error(err))
			}
		}
	}
	return nil
}

// extractZip 解压 zip 到指定目录
func extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := r.Close(); err != nil {
			logger.Default().Warn("close zip reader", zap.Error(err))
		}
	}()

	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name) //nolint:gosec // extracting trusted archive
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				logger.Default().Warn("create directory", zap.String("path", target), zap.Error(err))
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		outFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()) //nolint:gosec // extracting trusted archive
		if err != nil {
			if closeErr := rc.Close(); closeErr != nil {
				logger.Default().Warn("close zip entry after open error", zap.Error(closeErr))
			}
			return err
		}
		_, err = io.Copy(outFile, rc) //nolint:gosec // trusted archive source
		if closeErr := outFile.Close(); closeErr != nil {
			logger.Default().Warn("close extracted file", zap.Error(closeErr))
		}
		if closeErr := rc.Close(); closeErr != nil {
			logger.Default().Warn("close zip entry", zap.Error(closeErr))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// copyFile 复制文件
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // copying trusted file
	if err != nil {
		return err
	}
	defer func() {
		if err := in.Close(); err != nil {
			logger.Default().Warn("close source file", zap.String("path", src), zap.Error(err))
		}
	}()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm) //nolint:gosec // copying trusted file
	if err != nil {
		return err
	}
	defer func() {
		if err := out.Close(); err != nil {
			logger.Default().Warn("close destination file", zap.String("path", dst), zap.Error(err))
		}
	}()

	_, err = io.Copy(out, in)
	return err
}

// progressReader 带进度回调的 reader
type progressReader struct {
	r          io.Reader
	total      int64
	downloaded int64
	onProgress func(downloaded, total int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.downloaded += int64(n)
	pr.onProgress(pr.downloaded, pr.total)
	return n, err
}

// parseChecksums 解析 SHA256SUMS.txt 内容
// 格式: "<sha256hex>  <filename>" 或 "<sha256hex> <filename>" (sha256sum 输出格式)
func parseChecksums(content string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// sha256sum 输出格式: hash + 两个空格 + 文件名，也兼容单空格
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		hash := parts[0]
		filename := parts[1]
		// sha256sum 在二进制模式下文件名前可能有 * 前缀
		filename = strings.TrimPrefix(filename, "*")
		result[filename] = hash
	}
	return result
}

// compareVersions 比较两个版本号，支持预发布后缀
// 如 "1.0.0" vs "1.0.0-beta.1"，"1.0.0-beta.1" vs "1.0.0-beta.2"
// 返回: >0 表示 a 更新, <0 表示 b 更新, 0 表示相同
func compareVersions(a, b string) int {
	aBase, aPre := splitPreRelease(a)
	bBase, bPre := splitPreRelease(b)

	result := compareBase(aBase, bBase)
	if result != 0 {
		return result
	}

	// 同基础版本: 无预发布 > 有预发布 (stable > beta)
	if aPre == "" && bPre != "" {
		return 1
	}
	if aPre != "" && bPre == "" {
		return -1
	}
	if aPre == "" && bPre == "" {
		return 0
	}

	return comparePreRelease(aPre, bPre)
}

// splitPreRelease 分离基础版本和预发布后缀
// "1.0.0-beta.1" -> ("1.0.0", "beta.1")
func splitPreRelease(v string) (string, string) {
	idx := strings.Index(v, "-")
	if idx < 0 {
		return v, ""
	}
	return v[:idx], v[idx+1:]
}

// compareBase 比较基础版本号 (如 "1.0.0" vs "0.2.0")
func compareBase(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		var aNum, bNum int
		if i < len(aParts) {
			aNum, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bNum, _ = strconv.Atoi(bParts[i])
		}
		if aNum != bNum {
			return aNum - bNum
		}
	}
	return 0
}

// comparePreRelease 比较预发布标识符
// "beta.1" vs "beta.2", "beta.1" vs "rc.1"
func comparePreRelease(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		var ap, bp string
		if i < len(aParts) {
			ap = aParts[i]
		}
		if i < len(bParts) {
			bp = bParts[i]
		}

		aNum, aErr := strconv.Atoi(ap)
		bNum, bErr := strconv.Atoi(bp)
		if aErr == nil && bErr == nil {
			if aNum != bNum {
				return aNum - bNum
			}
		} else if ap != bp {
			return strings.Compare(ap, bp)
		}
	}
	return 0
}
