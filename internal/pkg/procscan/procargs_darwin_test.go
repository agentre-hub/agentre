//go:build darwin

package procscan

import (
	"encoding/binary"
	"reflect"
	"testing"
)

// buildProcArgs 按 kern.procargs2 的布局拼一段样本：argc、exec_path、NUL 填充、argv、env。
func buildProcArgs(argc int32, execPath string, pad int, argv, env []string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(argc))
	b = append(b, execPath...)
	b = append(b, 0)
	for i := 0; i < pad; i++ {
		b = append(b, 0)
	}
	for _, a := range argv {
		b = append(b, a...)
		b = append(b, 0)
	}
	for _, e := range env {
		b = append(b, e...)
		b = append(b, 0)
	}
	return b
}

func TestParseProcArgs(t *testing.T) {
	t.Run("提取 argv 与只有标记的环境值", func(t *testing.T) {
		buf := buildProcArgs(2, "/bin/sleep", 3, []string{"sleep", "30"},
			[]string{"PATH=/usr/bin", "SECRET=hunter2", "AGENTRE_SESSION=conv-1", "HOME=/x"})
		argv, marker, err := parseProcArgs(buf)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(argv, []string{"sleep", "30"}) || marker != "conv-1" {
			t.Fatalf("got %q %q", argv, marker)
		}
	})
	t.Run("没有环境", func(t *testing.T) {
		argv, marker, err := parseProcArgs(buildProcArgs(1, "/bin/x", 0, []string{"x"}, nil))
		if err != nil || marker != "" || !reflect.DeepEqual(argv, []string{"x"}) {
			t.Fatalf("got %q %q %v", argv, marker, err)
		}
	})
	t.Run("没有标记", func(t *testing.T) {
		_, marker, err := parseProcArgs(buildProcArgs(1, "/bin/x", 1, []string{"x"}, []string{"A=1", "AGENTRE_SESSIONX=2"}))
		if err != nil || marker != "" {
			t.Fatalf("marker=%q err=%v", marker, err)
		}
	})
	t.Run("环境尾部被截断的片段不当作标记", func(t *testing.T) {
		buf := buildProcArgs(1, "/bin/x", 0, []string{"x"}, []string{"AGENTRE_SESSION=conv-1"})
		buf = buf[:len(buf)-1-3] // 去掉结尾 NUL 与部分值
		_, marker, err := parseProcArgs(buf)
		if err != nil || marker != "" {
			t.Fatalf("marker=%q err=%v", marker, err)
		}
	})
	t.Run("缓冲区太短或 argv 不全报错", func(t *testing.T) {
		for name, buf := range map[string][]byte{
			"空":             nil,
			"只有 argc":       {1, 0, 0, 0},
			"exec_path 未结束": append([]byte{1, 0, 0, 0}, "/bin/x"...),
			"argv 少于 argc":  buildProcArgs(3, "/bin/x", 0, []string{"x"}, nil),
		} {
			if _, _, err := parseProcArgs(buf); err == nil {
				t.Errorf("%s: want error", name)
			}
		}
	})
}

func TestParseLsofPorts(t *testing.T) {
	out := "p100\ncnode\nn127.0.0.1:3000\nn[::1]:3000\nn*:8080\np200\ncpython\nn[::]:5000\nn127.0.0.1:5000\n"
	got := parseLsofPorts([]byte(out))
	want := map[int][]uint16{100: {3000, 8080}, 200: {5000}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if len(parseLsofPorts(nil)) != 0 || len(parseLsofPorts([]byte("pabc\nnx:y\n"))) != 0 {
		t.Fatal("垃圾输入应得到空结果")
	}
}

func TestParseLsofCwd(t *testing.T) {
	out := "p100\nfcwd\nn/Users/a b/proj\np200\nfcwd\nn/tmp\np300\nfcwd\n"
	got := parseLsofCwd([]byte(out))
	want := map[int]string{100: "/Users/a b/proj", 200: "/tmp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
