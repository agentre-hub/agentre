//go:build darwin

package procscan

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const helperEnv = "PROCSCAN_TEST_LISTEN_HELPER"

// TestMain 让测试二进制兼任"监听一个端口并睡眠"的辅助进程。
func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(2)
		}
		fmt.Println(ln.Addr().(*net.TCPAddr).Port)
		time.Sleep(2 * time.Minute) // 兜底：测试清理失败时也会自行退出
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDarwinRealProcessListAndKill(t *testing.T) {
	conv := fmt.Sprintf("conv-%d-%d", os.Getpid(), time.Now().UnixNano())
	portFile := t.TempDir() + "/port"
	// 像 agent 的 shell 一样用子 shell 起后台进程，让它脱离启动者。
	script := fmt.Sprintf(`( %q > %q & )`, os.Args[0], portFile)
	cmd := exec.Command("/bin/sh", "-c", script) //nolint:gosec // 测试自己拼的脚本
	cmd.Env = append(os.Environ(), helperEnv+"=1", MarkerEnv+"="+conv)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	sc := New()
	var port uint16
	var pid int
	// 进程可能已被我们杀掉，按端口文件里的端口找 pid 仅用于清理。
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if pid == 0 {
			if l, err := sc.List(ctx, conv); err == nil && len(l) > 0 {
				pid = l[0].PID
			}
		}
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL) // 仅按精确 PID
		}
	})

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(portFile) //nolint:gosec // 测试临时目录内的文件
		if err == nil {
			if line, _, _ := strings.Cut(string(b), "\n"); line != "" {
				if v, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
					port = uint16(v)
					break
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if port == 0 {
		t.Fatal("辅助进程没有报告端口")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var got *Listed
	for time.Now().Before(deadline.Add(30 * time.Second)) {
		l, err := sc.List(ctx, conv)
		if err != nil {
			t.Fatal(err)
		}
		if len(l) == 1 {
			got = &l[0]
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("List 没有找到带标记的后台进程")
	}
	pid = got.PID
	if len(got.Ports) != 1 || got.Ports[0] != port {
		t.Fatalf("端口 = %v, 想要 [%d]", got.Ports, port)
	}
	if got.StartedAtMs == 0 || got.Cwd == "" {
		t.Fatalf("起始时间/cwd 缺失: %+v", got)
	}
	if other, err := sc.List(ctx, conv+"-other"); err != nil || len(other) != 0 {
		t.Fatalf("别的会话不应认领: %v %v", other, err)
	}

	if err := sc.Kill(ctx, conv, got.PID, got.StartedAtMs); err != nil {
		t.Fatal(err)
	}
	l, err := sc.List(ctx, conv)
	if err != nil || len(l) != 0 {
		t.Fatalf("Kill 后仍在: %v %v", l, err)
	}
}
