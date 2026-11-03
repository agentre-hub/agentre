//go:build linux

package procscan_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/agentre-hub/agentre/internal/pkg/procscan"
)

// helperReportEnv 非空时，本测试二进制充当「被脱离的测试服务器」：监听回环端口、
// 把 "<pid> <port>" 写进该文件后一直睡眠，等待被结束。
const helperReportEnv = "AGENTRE_PROCSCAN_HELPER_REPORT"

func TestMain(m *testing.M) {
	if report := os.Getenv(helperReportEnv); report != "" {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(2)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		tmp := report + ".tmp"
		if err := os.WriteFile(tmp, []byte(fmt.Sprintf("%d %d", os.Getpid(), port)), 0o600); err != nil { //nolint:gosec // 测试自己的临时路径
			os.Exit(3)
		}
		if err := os.Rename(tmp, report); err != nil { //nolint:gosec // 同上
			os.Exit(3)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// waitFor 有界轮询，超时返回 false。
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

func TestLinuxRealDetachedServer(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	conv := uuid.NewString()
	report := filepath.Join(t.TempDir(), "report")

	// 像 agent 的 shell 那样：子 shell 里后台启动再退出，服务器被过继、脱离 CLI 进程树。
	cmd := exec.Command("sh", "-c", `( "$0" >/dev/null 2>&1 & )`, exe) //nolint:gosec // 测试有意以变量 exe 启动自身
	cmd.Env = append(os.Environ(), procscan.MarkerEnv+"="+conv, helperReportEnv+"="+report)
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	var pid, port int
	if !waitFor(10*time.Second, func() bool {
		b, err := os.ReadFile(report) //nolint:gosec // 测试自己的临时路径
		if err != nil {
			return false
		}
		f := strings.Fields(string(b))
		if len(f) != 2 {
			return false
		}
		pid, _ = strconv.Atoi(f[0])
		port, _ = strconv.Atoi(f[1])
		return pid > 0 && port > 0
	}) {
		t.Fatal("helper did not report pid/port")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	sc := procscan.New()
	ctx := context.Background()

	var found procscan.Listed
	if !waitFor(10*time.Second, func() bool {
		list, err := sc.List(ctx, conv)
		if err != nil {
			return false
		}
		for _, l := range list {
			if l.PID == pid {
				found = l
				return true
			}
		}
		return false
	}) {
		t.Fatalf("pid %d not listed for conv %s", pid, conv)
	}
	hasPort := false
	for _, p := range found.Ports {
		if int(p) == port {
			hasPort = true
		}
	}
	if !hasPort {
		t.Fatalf("ports %v do not contain %d", found.Ports, port)
	}
	if found.StartedAtMs <= 0 || found.Cwd == "" {
		t.Fatalf("missing start time / cwd: %+v", found)
	}

	if err := sc.Kill(ctx, conv, pid, found.StartedAtMs); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if !waitFor(10*time.Second, func() bool {
		list, err := sc.List(ctx, conv)
		if err != nil {
			return false
		}
		for _, l := range list {
			if l.PID == pid {
				return false
			}
		}
		return true
	}) {
		t.Fatalf("pid %d still listed after Kill", pid)
	}
}
