package protorpc

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

const (
	CodeMethodNotFound = rpcerror.CodeMethodNotFound
	CodeInvalidParams  = rpcerror.CodeInvalidParams
	CodeInternal       = rpcerror.CodeInternal
	CodeCanceled       = rpcerror.CodeCanceled
)

var ErrConnClosed = errors.New("protorpc: connection closed")
var ErrResponseType = errors.New("protorpc: response type mismatch")

type connCtxKey struct{}

func ConnFromContext(ctx context.Context) *Conn {
	conn, _ := ctx.Value(connCtxKey{}).(*Conn)
	return conn
}

type AuthState struct {
	Authenticated     bool
	DeviceFingerprint string
	DeviceName        string
	AccountID         string
}

type Error = rpcerror.Error

type FrameConn interface {
	ReadFrame() ([]byte, error)
	WriteFrame([]byte) error
	Close() error
	Done() <-chan struct{}
}

type disconnectedFrameConn struct {
	done chan struct{}
	once sync.Once
}

func newDisconnectedFrameConn() *disconnectedFrameConn {
	return &disconnectedFrameConn{done: make(chan struct{})}
}

func (*disconnectedFrameConn) ReadFrame() ([]byte, error) { return nil, io.EOF }
func (*disconnectedFrameConn) WriteFrame([]byte) error    { return ErrConnClosed }
func (c *disconnectedFrameConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}
func (c *disconnectedFrameConn) Done() <-chan struct{} { return c.done }

type notificationHandler func(context.Context, *agentrewire.RpcNotification)
type notificationSubscriber func(context.Context, *agentrewire.RpcNotification) error

type notificationSubscription struct {
	id      uint64
	handler notificationSubscriber
}

type Registry struct {
	methodMu           sync.RWMutex
	methods            map[uint32]genericHandler
	notificationMu     sync.RWMutex
	nextNotificationID uint64
	notifications      []notificationSubscription
}

func NewRegistry() *Registry { return &Registry{methods: map[uint32]genericHandler{}} }
func (r *Registry) Clone() *Registry {
	clone := NewRegistry()
	r.methodMu.RLock()
	defer r.methodMu.RUnlock()
	for methodID, handler := range r.methods {
		clone.methods[methodID] = handler
	}
	r.notificationMu.RLock()
	clone.nextNotificationID = r.nextNotificationID
	clone.notifications = append([]notificationSubscription(nil), r.notifications...)
	r.notificationMu.RUnlock()
	return clone
}
func (r *Registry) RegisterNotification(h notificationHandler) {
	if h != nil {
		r.SubscribeNotification(func(ctx context.Context, notification *agentrewire.RpcNotification) error {
			h(ctx, notification)
			return nil
		})
	}
}

func (r *Registry) SubscribeNotification(handler notificationSubscriber) func() {
	if handler == nil {
		return func() {}
	}
	r.notificationMu.Lock()
	r.nextNotificationID++
	id := r.nextNotificationID
	r.notifications = append(r.notifications, notificationSubscription{id: id, handler: handler})
	r.notificationMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.notificationMu.Lock()
			defer r.notificationMu.Unlock()
			for i := range r.notifications {
				if r.notifications[i].id == id {
					r.notifications = append(r.notifications[:i], r.notifications[i+1:]...)
					return
				}
			}
		})
	}
}

func (r *Registry) dispatchNotification(ctx context.Context, notification *agentrewire.RpcNotification) {
	r.notificationMu.RLock()
	subscribers := append([]notificationSubscription(nil), r.notifications...)
	r.notificationMu.RUnlock()
	for _, subscriber := range subscribers {
		_ = subscriber.handler(ctx, notification)
	}
}

type result struct {
	response *agentrewire.Response
	rpcErr   *agentrewire.RpcError
}

type Conn struct {
	transport  FrameConn
	registry   *Registry
	nextID     atomic.Uint64
	writeMu    sync.Mutex
	pendingMu  sync.Mutex
	pending    map[uint64]chan result
	inflightMu sync.Mutex
	inflight   map[uint64]context.CancelFunc
	canceled   map[uint64]struct{}
	closeOnce  sync.Once
	closed     chan struct{}
	authMu     sync.RWMutex
	auth       AuthState
}

func NewConn(t FrameConn, r *Registry) *Conn {
	if t == nil {
		t = newDisconnectedFrameConn()
	}
	return &Conn{transport: t, registry: r, pending: map[uint64]chan result{}, inflight: map[uint64]context.CancelFunc{}, canceled: map[uint64]struct{}{}, closed: make(chan struct{})}
}

func (c *Conn) Registry() *Registry   { return c.registry }
func (c *Conn) Done() <-chan struct{} { return c.transport.Done() }
func (c *Conn) Auth() AuthState {
	c.authMu.RLock()
	defer c.authMu.RUnlock()
	return c.auth
}
func (c *Conn) SetAuth(auth AuthState) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	c.auth = auth
}

func (c *Conn) Serve(ctx context.Context) {
	// 请求生命周期归这条连接所有,不归调用方的 ctx。拨号 / 升级的调用方通常只把
	// ctx 当握手期限用(RaceProtobuf 选出赢家后就把它自己那条拨号 ctx 一起取消),
	// 若直接拿它派发请求,对端反向发来的每个请求一进 handler 就已经是 canceled。
	// 因此这里剥掉调用方的取消、只保留其携带的值,改由读循环退出时统一收口:
	// 传输一断,所有在飞 handler 立即被取消,不会挂在死连接上跑完。
	requestCtx, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRequests()
	defer func() { _ = c.Close() }()
	for {
		b, err := c.transport.ReadFrame()
		if err != nil {
			return
		}
		var f agentrewire.RpcFrame
		if proto.Unmarshal(b, &f) != nil {
			continue
		}
		switch body := f.Body.(type) {
		case *agentrewire.RpcFrame_Response:
			c.deliver(f.Id, result{response: body.Response})
		case *agentrewire.RpcFrame_Error:
			c.deliver(f.Id, result{rpcErr: body.Error})
		case *agentrewire.RpcFrame_Cancel:
			c.cancel(body.Cancel.RequestId)
		case *agentrewire.RpcFrame_Request:
			c.markDispatched(f.Id)
			go c.handle(requestCtx, f.Id, body.Request)
		case *agentrewire.RpcFrame_Notification:
			c.registry.dispatchNotification(context.WithValue(requestCtx, connCtxKey{}, c), body.Notification)
		}
	}
}

func (c *Conn) handle(parent context.Context, id uint64, req *agentrewire.Request) {
	ctx, cancel := context.WithCancel(context.WithValue(parent, connCtxKey{}, c))
	c.inflightMu.Lock()
	c.inflight[id] = cancel
	_, wasCanceled := c.canceled[id]
	delete(c.canceled, id)
	c.inflightMu.Unlock()
	if wasCanceled {
		cancel()
	}
	defer func() { cancel(); c.inflightMu.Lock(); delete(c.inflight, id); c.inflightMu.Unlock() }()
	payload, err := c.registry.dispatchMethod(ctx, req.GetMethodId(), req.GetEncodedPayload())
	if err != nil {
		c.writeError(id, err)
		return
	}
	_ = c.write(&agentrewire.RpcFrame{Id: id, Body: &agentrewire.RpcFrame_Response{Response: &agentrewire.Response{MethodId: req.GetMethodId(), EncodedPayload: payload}}})
}

func (c *Conn) writeError(id uint64, err error) {
	code := CodeInternal
	var rpcErr *Error
	if errors.As(err, &rpcErr) {
		code = rpcErr.Code
	}
	if errors.Is(err, context.Canceled) {
		code = CodeCanceled
	}
	_ = c.write(&agentrewire.RpcFrame{Id: id, Body: &agentrewire.RpcFrame_Error{Error: &agentrewire.RpcError{Code: code, Message: err.Error()}}})
}

func methodNotFound() error { return &Error{Code: CodeMethodNotFound, Message: "method not found"} }

func (c *Conn) call(ctx context.Context, req *agentrewire.Request) (*agentrewire.Response, error) {
	id := c.nextID.Add(1)
	ch := make(chan result, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	defer func() { c.pendingMu.Lock(); delete(c.pending, id); c.pendingMu.Unlock() }()
	if err := c.write(&agentrewire.RpcFrame{Id: id, Body: &agentrewire.RpcFrame_Request{Request: req}}); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r.rpcErr != nil {
			return nil, &Error{Code: r.rpcErr.Code, Message: r.rpcErr.Message, Details: r.rpcErr.Details}
		}
		return r.response, nil
	case <-ctx.Done():
		_ = c.write(&agentrewire.RpcFrame{Body: &agentrewire.RpcFrame_Cancel{Cancel: &agentrewire.Cancel{RequestId: id}}})
		return nil, ctx.Err()
	case <-c.closed:
		return nil, ErrConnClosed
	case <-c.transport.Done():
		return nil, ErrConnClosed
	}
}

func (c *Conn) Notify(notification *agentrewire.RpcNotification) error {
	return c.write(&agentrewire.RpcFrame{Body: &agentrewire.RpcFrame_Notification{Notification: notification}})
}

func (c *Conn) write(f *agentrewire.RpcFrame) error {
	b, e := proto.Marshal(f)
	if e != nil {
		return e
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.closed:
		return ErrConnClosed
	default:
	}
	if e = c.transport.WriteFrame(b); e != nil {
		return ErrConnClosed
	}
	return nil
}
func (c *Conn) deliver(id uint64, r result) {
	c.pendingMu.Lock()
	ch := c.pending[id]
	c.pendingMu.Unlock()
	if ch != nil {
		select {
		case ch <- r:
		default:
		}
	}
}

// markDispatched 在读循环里同步记下「这个请求 ID 已经派发给 handler goroutine」,
// 占位的 nil CancelFunc 会被 handle 换成真正的 cancel。缺了这一步,cancel 就分不清
// 「handler 还没排到」和「handler 早就跑完了」,只能对两者一律记预取消。
func (c *Conn) markDispatched(id uint64) {
	c.inflightMu.Lock()
	c.inflight[id] = nil
	c.inflightMu.Unlock()
}

func (c *Conn) cancel(id uint64) {
	c.inflightMu.Lock()
	cancel, dispatched := c.inflight[id]
	if dispatched && cancel == nil {
		// 已派发但 handler goroutine 还没注册 cancel:先把取消意图记下来,
		// 由 handle 启动时消费。这个条目的寿命只覆盖 go 语句到 handler 首行
		// 之间的调度窗口,而 handle 一定会跑到并删除它,所以 canceled 有界。
		c.canceled[id] = struct{}{}
	}
	c.inflightMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (c *Conn) Close() error {
	var err error
	c.closeOnce.Do(func() { close(c.closed); err = c.transport.Close() })
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
