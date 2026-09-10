package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/agents/provider"
)

type Client struct {
	binary       string
	cwd          string
	env          map[string]string
	model        string
	systemPrompt string
	sandbox      SandboxMode
	approval     ApprovalPolicy
	config       []string
	extraArgs    []string
	killGrace    time.Duration
	runner       appServerRunner

	// rawSink 若非 nil,app-server 每读到一行原始 stdout(未解析的 JSON-RPC 帧)就同步
	// 回调一次。debug 级原始帧转储用;经 startApp → newAppClient 注入 appClient.readLoop。
	rawSink func([]byte)
}

// Session is a persistent codex app-server process that can host multiple
// turns for one Agentre chat session. Turns are still serialized and exposed as
// Stream values; closing the Session terminates the underlying app-server.
type Session struct {
	client *Client
	app    *appClient

	mu          sync.Mutex
	turnMu      sync.Mutex
	sid         string
	model       string
	threadReady bool
	closed      bool
	active      *Stream
	history     *turnHistory
}

func New(opts ...Option) *Client {
	c := &Client{
		binary:    "codex",
		approval:  ApprovalNever,
		killGrace: 10 * time.Second,
		runner:    execAppServerRunner{},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) Stream(ctx context.Context, prompt string, opts ...RunOption) (*Stream, error) {
	return c.StreamInput(ctx, userInput(prompt), opts...)
}

func (c *Client) StreamInput(ctx context.Context, input []UserInput, opts ...RunOption) (*Stream, error) {
	spec := c.defaultRunSpec()
	for _, o := range opts {
		o(&spec)
	}
	app, err := c.startApp(ctx)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = app.terminate(context.Background(), c.killGrace)
	}
	if err := initializeApp(ctx, app); err != nil {
		cleanup()
		return nil, err
	}
	thread, err := c.startOrResumeThread(ctx, app, spec)
	if err != nil {
		cleanup()
		return nil, err
	}
	turnParams, err := turnStartParamsInput(thread, input, spec.collaborationMode, c.model)
	if err != nil {
		cleanup()
		return nil, err
	}
	raw, err := app.Call(ctx, appMethodTurnStart, turnParams)
	if err != nil {
		cleanup()
		return nil, err
	}
	var turn appTurnStartResponse
	if err := json.Unmarshal(raw, &turn); err != nil {
		cleanup()
		return nil, err
	}
	if turn.Turn.ID == "" {
		cleanup()
		return nil, errors.New("codex: turn/start response missing id")
	}
	stream := newStream(app, c.killGrace, thread.ThreadID, turn.Turn.ID, "")
	// The stream is intentionally scoped to the caller's context so cancellation interrupts the active turn.
	go stream.drain(ctx) //nolint:gosec // G118: request context defines this goroutine's lifecycle.
	return stream, nil
}

func (c *Client) Compact(ctx context.Context, threadID string) (*Stream, error) {
	if strings.TrimSpace(threadID) == "" {
		return nil, errors.New("codex: compact thread id is required")
	}
	app, err := c.startApp(ctx)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = app.terminate(context.Background(), c.killGrace)
	}
	if err := initializeApp(ctx, app); err != nil {
		cleanup()
		return nil, err
	}
	thread, err := c.startOrResumeThread(ctx, app, runSpec{
		resumeID: strings.TrimSpace(threadID),
		cwd:      c.cwd,
		sandbox:  c.sandbox,
		approval: c.approval,
	})
	if err != nil {
		cleanup()
		return nil, err
	}
	if _, err := app.Call(ctx, appMethodThreadCompact, map[string]any{
		"threadId": thread.ThreadID,
	}); err != nil {
		cleanup()
		return nil, err
	}
	stream := newStream(app, c.killGrace, thread.ThreadID, "", "manual")
	// Compaction follows the caller's context; cancellation must stop the app-server stream.
	go stream.drain(ctx) //nolint:gosec // G118: request context defines this goroutine's lifecycle.
	return stream, nil
}

func (c *Client) OpenSession(ctx context.Context, opts ...RunOption) (*Session, error) {
	spec := c.defaultRunSpec()
	for _, o := range opts {
		o(&spec)
	}
	app, err := c.startApp(ctx)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = app.terminate(context.Background(), c.killGrace)
	}
	if err := initializeApp(ctx, app); err != nil {
		cleanup()
		return nil, err
	}
	return &Session{client: c, app: app, sid: spec.resumeID, history: newTurnHistory()}, nil
}

func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sid
}

func (s *Session) Model() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model
}

func (s *Session) Stream(ctx context.Context, prompt string, opts ...RunOption) (*Stream, error) {
	return s.StreamInput(ctx, userInput(prompt), opts...)
}

func (s *Session) StreamInput(ctx context.Context, input []UserInput, opts ...RunOption) (*Stream, error) {
	s.turnMu.Lock()
	spec := s.client.defaultRunSpec()
	for _, o := range opts {
		o(&spec)
	}
	if strings.TrimSpace(spec.resumeID) != "" {
		s.mu.Lock()
		s.sid = strings.TrimSpace(spec.resumeID)
		s.model = ""
		s.threadReady = false
		s.mu.Unlock()
	}
	thread, err := s.ensureThread(ctx, spec)
	if err != nil {
		s.turnMu.Unlock()
		return nil, err
	}
	turnParams, err := turnStartParamsInput(thread, input, spec.collaborationMode, s.client.model)
	if err != nil {
		s.turnMu.Unlock()
		return nil, err
	}
	raw, err := s.app.Call(ctx, appMethodTurnStart, turnParams)
	if err != nil {
		s.turnMu.Unlock()
		return nil, err
	}
	var turn appTurnStartResponse
	if err := json.Unmarshal(raw, &turn); err != nil {
		s.turnMu.Unlock()
		return nil, err
	}
	if turn.Turn.ID == "" {
		s.turnMu.Unlock()
		return nil, errors.New("codex: turn/start response missing id")
	}
	stream := newStream(s.app, s.client.killGrace, thread.ThreadID, turn.Turn.ID, "")
	stream.closeAppOnDrain = false
	stream.history = s.history
	s.setActive(stream)
	go func() {
		defer s.turnMu.Unlock()
		stream.drain(ctx)
		s.clearActive(stream)
		if sid := stream.SessionID(); sid != "" {
			s.mu.Lock()
			s.sid = sid
			s.threadReady = true
			s.mu.Unlock()
		}
	}()
	return stream, nil
}

func (s *Session) Compact(ctx context.Context) (*Stream, error) {
	s.turnMu.Lock()
	threadID := strings.TrimSpace(s.ID())
	if threadID == "" {
		s.turnMu.Unlock()
		return nil, errors.New("codex: compact thread id is required")
	}
	thread, err := s.ensureThread(ctx, runSpec{
		resumeID: threadID,
		cwd:      s.client.cwd,
		sandbox:  s.client.sandbox,
		approval: s.client.approval,
	})
	if err != nil {
		s.turnMu.Unlock()
		return nil, err
	}
	if _, err := s.app.Call(ctx, appMethodThreadCompact, map[string]any{
		"threadId": thread.ThreadID,
	}); err != nil {
		s.turnMu.Unlock()
		return nil, err
	}
	stream := newStream(s.app, s.client.killGrace, thread.ThreadID, "", "manual")
	stream.closeAppOnDrain = false
	stream.history = s.history
	s.setActive(stream)
	go func() {
		defer s.turnMu.Unlock()
		stream.drain(ctx)
		s.clearActive(stream)
	}()
	return stream, nil
}

func (s *Session) RewindTo(ctx context.Context, anchor string) (string, error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if strings.TrimSpace(s.ID()) == "" {
		return "", errors.New("codex: missing thread id for rollback")
	}
	numTurns, err := strconv.Atoi(strings.TrimSpace(anchor))
	if err != nil || numTurns <= 0 {
		return "", errors.New("codex: thread/rollback numTurns must be >= 1")
	}
	thread, err := s.ensureThread(ctx, runSpec{
		resumeID: s.ID(),
		cwd:      s.client.cwd,
		sandbox:  s.client.sandbox,
		approval: s.client.approval,
	})
	if err != nil {
		return "", err
	}
	res, err := revertLastTurns(ctx, s.app, thread.ThreadID, numTurns)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.sid = res.Thread.ID
	s.threadReady = true
	s.mu.Unlock()
	return res.Thread.ID, nil
}

func (s *Session) ActiveStream() *Stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	app := s.app
	s.mu.Unlock()
	if app == nil {
		return nil
	}
	return app.terminate(ctx, s.client.killGrace)
}

func (c *Client) Text(ctx context.Context, prompt string, opts ...RunOption) (string, error) {
	stream, err := c.Stream(ctx, prompt, opts...)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	var stopErr error
	for stream.Next() {
		ev := stream.Event()
		switch ev.Kind {
		case EventTextDelta:
			b.WriteString(ev.Text)
		case EventError:
			if ev.Err != nil {
				stopErr = ev.Err
			}
		}
	}
	if err := stream.Close(ctx); err != nil && stopErr == nil {
		stopErr = err
	}
	if stopErr != nil {
		return "", stopErr
	}
	return b.String(), nil
}

type ForkThreadResult struct {
	ThreadID     string
	ForkedFromID string
}

func (c *Client) ForkThread(ctx context.Context, sourceThreadID string) (*ForkThreadResult, error) {
	spec := c.defaultRunSpec()
	app, err := c.startApp(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = app.terminate(context.Background(), c.killGrace) }()
	if err := initializeApp(ctx, app); err != nil {
		return nil, err
	}
	params := threadParams(c, spec)
	params["threadId"] = sourceThreadID
	raw, err := app.Call(ctx, appMethodThreadFork, params)
	if err != nil {
		return nil, err
	}
	var res appThreadResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if res.Thread.ID == "" {
		return nil, errors.New("codex: thread/fork response missing id")
	}
	return &ForkThreadResult{ThreadID: res.Thread.ID, ForkedFromID: res.Thread.ForkedFromID}, nil
}

type RollbackThreadResult struct {
	ThreadID string
}

func (c *Client) RollbackThread(ctx context.Context, threadID string, numTurns int) (*RollbackThreadResult, error) {
	if numTurns <= 0 {
		return nil, errors.New("codex: thread/rollback numTurns must be >= 1")
	}
	app, err := c.startApp(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = app.terminate(context.Background(), c.killGrace) }()
	if err := initializeApp(ctx, app); err != nil {
		return nil, err
	}
	if _, err := c.startOrResumeThread(ctx, app, runSpec{resumeID: threadID, cwd: c.cwd, sandbox: c.sandbox, approval: c.approval}); err != nil {
		return nil, err
	}
	res, err := revertLastTurns(ctx, app, threadID, numTurns)
	if err != nil {
		return nil, err
	}
	return &RollbackThreadResult{ThreadID: res.Thread.ID}, nil
}

type appThreadTurnsListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func revertLastTurns(ctx context.Context, app *appClient, threadID string, numTurns int) (appThreadResponse, error) {
	raw, err := app.Call(ctx, appMethodThreadTurnsList, map[string]any{
		"threadId":      threadID,
		"limit":         numTurns,
		"sortDirection": "desc",
		"itemsView":     "summary",
	})
	if err != nil {
		return appThreadResponse{}, err
	}
	var page appThreadTurnsListResponse
	if err := json.Unmarshal(raw, &page); err != nil {
		return appThreadResponse{}, err
	}
	if len(page.Data) < numTurns {
		return appThreadResponse{}, fmt.Errorf("codex: cannot revert %d turns: thread contains %d", numTurns, len(page.Data))
	}
	beforeTurnID := strings.TrimSpace(page.Data[numTurns-1].ID)
	if beforeTurnID == "" {
		return appThreadResponse{}, errors.New("codex: thread/turns/list response missing turn id")
	}
	raw, err = app.Call(ctx, appMethodThreadRevert, map[string]any{
		"threadId":     threadID,
		"beforeTurnId": beforeTurnID,
	})
	if err != nil {
		return appThreadResponse{}, err
	}
	var res appThreadResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return appThreadResponse{}, err
	}
	if res.Thread.ID == "" {
		return appThreadResponse{}, errors.New("codex: thread/revert response missing id")
	}
	return res, nil
}

func (c *Client) Close(_ context.Context) error { return nil }

func (c *Client) defaultRunSpec() runSpec {
	return runSpec{
		cwd:      c.cwd,
		sandbox:  c.sandbox,
		approval: c.approval,
	}
}

func (c *Client) startApp(ctx context.Context) (*appClient, error) {
	return newAppClient(ctx, c.runner, procOptions{
		Binary: c.binary,
		Args:   buildAppServerArgs(c.config, c.extraArgs),
		Cwd:    c.cwd,
		Env:    buildEnv(c.env),
	}, c.rawSink)
}

func initializeApp(ctx context.Context, app *appClient) error {
	if _, err := app.Call(ctx, appMethodInitialize, map[string]any{
		"clientInfo": map[string]any{
			"name":    "agentre",
			"title":   "Agentre",
			"version": "0.0.0",
		},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return err
	}
	return app.Notify(ctx, appMethodInitialized, nil)
}

func (c *Client) startOrResumeThread(ctx context.Context, app *appClient, spec runSpec) (appThreadStartResult, error) {
	params := threadParams(c, spec)
	method := appMethodThreadStart
	if spec.resumeID != "" {
		method = appMethodThreadResume
		params["threadId"] = spec.resumeID
		params["excludeTurns"] = true
	}
	raw, err := app.Call(ctx, method, params)
	if err != nil {
		return appThreadStartResult{}, err
	}
	var res appThreadResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return appThreadStartResult{}, err
	}
	if res.Thread.ID != "" {
		return appThreadStartResult{ThreadID: res.Thread.ID, Model: res.Model}, nil
	}
	return appThreadStartResult{}, errors.New("codex: thread response missing id")
}

func (s *Session) ensureThread(ctx context.Context, spec runSpec) (appThreadStartResult, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return appThreadStartResult{}, errors.New("codex: session closed")
	}
	if s.threadReady && strings.TrimSpace(s.sid) != "" {
		thread := appThreadStartResult{ThreadID: s.sid, Model: s.model}
		s.mu.Unlock()
		return thread, nil
	}
	if spec.resumeID == "" {
		spec.resumeID = s.sid
	}
	s.mu.Unlock()

	thread, err := s.client.startOrResumeThread(ctx, s.app, spec)
	if err != nil {
		return appThreadStartResult{}, err
	}
	s.mu.Lock()
	s.sid = thread.ThreadID
	s.model = strings.TrimSpace(thread.Model)
	s.threadReady = true
	s.mu.Unlock()
	return thread, nil
}

func (s *Session) setActive(stream *Stream) {
	s.mu.Lock()
	s.active = stream
	s.mu.Unlock()
}

func (s *Session) clearActive(stream *Stream) {
	s.mu.Lock()
	if s.active == stream {
		s.active = nil
	}
	s.mu.Unlock()
}

type Stream struct {
	app       *appClient
	killGrace time.Duration
	events    chan Event

	mu            sync.RWMutex
	sessionID     string
	turnID        string
	cur           Event
	err           error
	usage         provider.Usage
	contextWindow int
	state         *turnStateMachine
	reusable      bool
	history       *turnHistory

	userInputMu       sync.Mutex
	userInputRequests map[string]json.RawMessage
	approvalRequests  map[string]approvalRequest
	compactSeen       map[string]struct{}
	completedItems    map[string]struct{}
	planText          map[string]string
	compactTrigger    string
	interruptSignal   chan struct{}

	closeOnce       sync.Once
	closeAppOnDrain bool
}

func newStream(app *appClient, killGrace time.Duration, threadID, turnID, compactTrigger string) *Stream {
	state := newTurnStateMachine()
	_, _ = state.Transition(TurnStateRunning)
	return &Stream{
		app:               app,
		killGrace:         killGrace,
		events:            make(chan Event, 64),
		sessionID:         threadID,
		turnID:            turnID,
		userInputRequests: map[string]json.RawMessage{},
		approvalRequests:  map[string]approvalRequest{},
		compactSeen:       map[string]struct{}{},
		completedItems:    map[string]struct{}{},
		planText:          map[string]string{},
		compactTrigger:    compactTrigger,
		interruptSignal:   make(chan struct{}, 1),
		state:             state,
		reusable:          true,
		history:           newTurnHistory(),
		closeAppOnDrain:   true,
	}
}

func (s *Stream) Next() bool {
	ev, ok := <-s.events
	if !ok {
		return false
	}
	s.cur = ev
	return true
}

func (s *Stream) Event() Event { return s.cur }

func (s *Stream) SessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

func (s *Stream) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}

func (s *Stream) State() TurnState {
	if s == nil || s.state == nil {
		return TurnStateFailed
	}
	return s.state.State()
}

// Reusable reports whether a persistent Session may safely start another turn
// on this Stream's app-server process.
func (s *Stream) Reusable() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reusable
}

func (s *Stream) Close(ctx context.Context) error {
	if !s.closeAppOnDrain {
		return s.Err()
	}
	var err error
	s.closeOnce.Do(func() {
		err = s.app.terminate(ctx, s.killGrace)
	})
	if err != nil {
		return err
	}
	return s.Err()
}
