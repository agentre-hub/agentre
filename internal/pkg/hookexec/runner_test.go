package hookexec

import (
	"errors"
	"os/exec"
	"testing"
)

func TestResolve_UnknownInterpreter(t *testing.T) {
	if _, err := resolve("ruby", ""); !errors.Is(err, errUnknownInterpreter) {
		t.Fatalf("expected errUnknownInterpreter, got %v", err)
	}
}

func TestResolve_KnownInterpreterShape(t *testing.T) {
	// sh 在类 Unix CI 一定在；不在则跳过（Windows）。
	in, err := resolve("sh", "")
	if errors.Is(err, errInterpreterNotInstalled) {
		t.Skip("sh not installed on this platform")
	}
	if err != nil {
		t.Fatalf("Resolve sh: %v", err)
	}
	if in.Bin == "" || in.Ext != ".sh" {
		t.Fatalf("unexpected interp: %+v", in)
	}
}

func TestResolve_PwshArgs(t *testing.T) {
	in, err := resolve("pwsh", "")
	if errors.Is(err, errInterpreterNotInstalled) {
		t.Skip("pwsh not installed")
	}
	if err != nil {
		t.Fatalf("Resolve pwsh: %v", err)
	}
	if len(in.Args) == 0 || in.Args[len(in.Args)-1] != "-File" {
		t.Fatalf("pwsh should pass -File before script path: %+v", in.Args)
	}
}

func TestProbe_DarwinHidesWindowsOnly(t *testing.T) {
	got := Probe("darwin")
	keys := map[string]Available{}
	for _, a := range got {
		keys[a.Key] = a
	}
	for _, win := range []string{"cmd", "powershell"} {
		if _, ok := keys[win]; ok {
			t.Errorf("Probe(darwin) should hide windows-only %q", win)
		}
	}
	if _, ok := keys["sh"]; !ok {
		t.Fatal("Probe(darwin) should list sh")
	}
	if !keys["sh"].Installed || keys["sh"].Path == "" {
		t.Errorf("sh should resolve on unix CI, got %+v", keys["sh"])
	}
}

func TestProbe_WindowsListsCmd(t *testing.T) {
	got := Probe("windows")
	var hasCmd bool
	for _, a := range got {
		if a.Key == "cmd" {
			hasCmd = true
		}
	}
	if !hasCmd {
		t.Error("Probe(windows) should list cmd")
	}
}

func TestResolve_PathOverrideUsesGivenBinary(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not on PATH")
	}
	in, err := resolve("python", sh) // 借 sh 当 python 二进制,验证「路径覆盖」生效
	if err != nil {
		t.Fatalf("Resolve override: %v", err)
	}
	if in.Bin != sh {
		t.Errorf("Bin = %q, want override %q", in.Bin, sh)
	}
	if in.Ext != ".py" {
		t.Errorf("Ext = %q, want preset .py", in.Ext) // args/ext 仍取自预设
	}
}

func TestResolve_PathOverrideMissingFile(t *testing.T) {
	_, err := resolve("python", "/no/such/bin")
	if !errors.Is(err, errInterpreterNotInstalled) {
		t.Errorf("err = %v, want errInterpreterNotInstalled", err)
	}
}
