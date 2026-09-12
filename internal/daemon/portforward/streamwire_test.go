package portforward

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// auth 是宿主自己那道闸门（两种执行端的拒绝语不是同一句，所以它是参数）。它缺席时
// 必须与 gate 缺席同样处理：**整族不注册**。
//
// 「不注册」就是「不可达」——注册是方法能被调用的唯一途径，所以这条断言与
// 「一条不问『你是谁』的通路办不到」是同一句话。原先这里补了一个恒放行的默认值，
// 于是「调用方忘了传闸门」的后果不是办不到，而是**任何能连上这条连接的人都能开转发
// 流**；同一个函数对 gate 缺席是 fail-closed，对 auth 缺席是 fail-open，两者不对称
// 才是最该修的地方。
func TestRegisterStreamMethodsRegistersNothingWithoutAuth(t *testing.T) {
	registry := protorpc.NewRegistry()
	streams := NewStreams(StreamOptions{Gate: NewHandlers(Options{})})

	RegisterStreamMethods(registry, streams, nil)

	if got := registry.RegisteredMethods(); len(got) != 0 {
		t.Errorf("auth 缺席时注册了 %d 个方法（%v）；调用方应收到 method not found，"+
			"而不是拿到一条没人把关的通路", len(got), got)
	}
}

// 对照：闸门与 auth 都在时四个方法都挂上。
//
// 没有这一条，上面那条会因为「方法号写错了、一个都没注册」而假绿。
func TestRegisterStreamMethodsRegistersTheWholeFamily(t *testing.T) {
	registry := protorpc.NewRegistry()
	streams := NewStreams(StreamOptions{Gate: NewHandlers(Options{})})

	RegisterStreamMethods(registry, streams, func(context.Context) error { return nil })

	if got := registry.RegisteredMethods(); len(got) != 4 {
		t.Errorf("gate 与 auth 都在时应当挂上 open / write / close / ack 四个方法，实得 %d 个：%v", len(got), got)
	}
}

// 闸门缺席本来就不注册（既有行为，一并在同一处守住）。
func TestRegisterStreamMethodsRegistersNothingWithoutGate(t *testing.T) {
	registry := protorpc.NewRegistry()

	RegisterStreamMethods(registry, NewStreams(StreamOptions{}), func(context.Context) error { return nil })

	if got := registry.RegisteredMethods(); len(got) != 0 {
		t.Errorf("gate 缺席时注册了 %d 个方法（%v）", len(got), got)
	}
}
