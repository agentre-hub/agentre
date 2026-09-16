package workspacefs

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoCwd 表示调用 ReadFile 时未提供工作目录(root 为空)。映射到既有错误码
// WorkspaceFsNoCwd(20800 段),与"越界"的 ErrPathRefused 区分开——cwd 为空是
// 会话配置问题,不是路径问题。
var ErrNoCwd = errors.New("workspacefs: no cwd")

// maxImageSize 是图片(扩展名 allowlist)被判定为过大前允许读取的字节上限。
// 与文本阈值 maxUntrackedTextSize 分开:图片是唯一允许传输的二进制,阈值单列,
// 给真实照片留余量(spec 设计决策 4)。
const maxImageSize = 10 << 20 // 10 MiB

// ReadFileResult 是 ReadFile 的返回结果。
type ReadFileResult struct {
	Content     string // 文本为 UTF-8 正文;图片为 base64 编码;binary/tooLarge 时恒为空
	ContentType string // 图片的 MIME(如 image/png、image/svg+xml);文本恒为空串
	Binary      bool   // 非图片二进制(内容含 NUL);为 true 时 Content 为空
	TooLarge    bool   // 超过各自类型阈值(文本 1 MiB / 图片 10 MiB),未读取内容
}

// ReadFile 读取 root 下 relPath 所指文件的内容(会话级文件预览,纯读)。
//   - root 是会话已解析的绝对工作目录,是路径越界校验的信任边界;root 为
//     空串 → ErrNoCwd。
//   - relPath 相对 root;含 ".."、绝对路径,或跟随符号链接解析后逃出 root 的
//     请求一律拒绝为 ErrPathRefused(与 ListDir 的 resolveRelPath 同源,额外
//     多一道符号链接跟随后的重校验——读文件比列目录更需要防"链接逃逸出 cwd
//     再读出内容")。
//   - 目录 / 非常规文件(符号链接已解析后)同样拒绝为 ErrPathRefused:本包错误
//     族里没有"非文件"档,复用一个明确哨兵让调用方统一映射到
//     WorkspaceFsPathRefused,而不是新开错误码段。
//   - 文本(markdown / 代码 / 文本):>1MiB 判 TooLarge 不整读;≤1MiB 读入后含
//     NUL 判 Binary 不返回正文,否则返回 UTF-8 正文。
//   - 图片:扩展名 allowlist(png/jpg/jpeg/gif/webp/avif/bmp/ico/svg)判定,是
//     图片则 >10MiB 判 TooLarge,否则返回 base64 内容 + MIME;图片是唯一允许
//     传输的二进制(设计决策 4)。
//   - 纯读:不写文件、不改 git 状态;正文与图片内容不进日志(本包无日志)。
func ReadFile(ctx context.Context, root, relPath string) (*ReadFileResult, error) {
	if root == "" {
		return nil, ErrNoCwd
	}
	path, err := resolveRelPath(root, relPath)
	if err != nil {
		return nil, err
	}

	// 跟随符号链接后再校验仍落在 root 之内:链接逃逸出 cwd 的文件内容不能经本
	// 接口读出。root 自身可能含链接,故两侧都 EvalSymlinks 再比较。
	resolved, err := resolveWithinRoot(root, path, relPath, false)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("workspacefs: stat %q: %w", relPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, ErrPathRefused
	}

	mime, isImage := imageMIME(relPath)
	limit := int64(maxUntrackedTextSize)
	if isImage {
		limit = maxImageSize
	}
	if info.Size() > limit {
		return &ReadFileResult{TooLarge: true}, nil
	}

	data, err := os.ReadFile(resolved) //nolint:gosec // G304: resolved 已由 resolveWithinRoot 校验落在 root 内
	if err != nil {
		return nil, fmt.Errorf("workspacefs: read %q: %w", relPath, err)
	}
	if isImage {
		return &ReadFileResult{
			Content:     base64.StdEncoding.EncodeToString(data),
			ContentType: mime,
		}, nil
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return &ReadFileResult{Binary: true}, nil
	}
	return &ReadFileResult{Content: string(data)}, nil
}

// pathWithin 判定 path 与 root 相同或位于 root 子树内(路径边界校验)。
func pathWithin(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

// resolveWithinRoot 跟随 path 的符号链接后校验结果仍落在 root 之内,返回解析后
// 的路径。ReadFile 与 GitFileContent 共用这同一道链接逃逸闸门。
//
// allowMissing 为 true 时,EvalSymlinks 报 ENOENT(工作区已删除、只在 HEAD 里
// 存在的文件)不算越界,返回空串交由调用方跳过重校验。
func resolveWithinRoot(root, path, relPath string, allowMissing bool) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if allowMissing && errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("workspacefs: resolve %q: %w", relPath, err)
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("workspacefs: resolve cwd: %w", err)
	}
	if !pathWithin(rootResolved, resolved) {
		return "", ErrPathRefused
	}
	return resolved, nil
}

// imageMIME 按扩展名 allowlist 判定文件是否为图片,并返回其 MIME。判定基于
// 调用方传入的 relPath(与前端预览按钮的 allowlist 一致),大小写不敏感。svg
// 返回 image/svg+xml(经 <img> 渲染,img 上下文不执行脚本,防 XSS)。
func imageMIME(name string) (string, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".avif":
		return "image/avif", true
	case ".bmp":
		return "image/bmp", true
	case ".ico":
		return "image/x-icon", true
	case ".svg":
		return "image/svg+xml", true
	default:
		return "", false
	}
}
