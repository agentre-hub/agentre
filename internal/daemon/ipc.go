package daemon

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/cago-frame/cago/configs"

	"github.com/agentre-hub/agentre/internal/buildinfo"
	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/agentredipc"
)

// BuildIdentity returns the version and commit injected into the agentred binary.
func BuildIdentity() string {
	return configs.Version + " (" + buildinfo.ShortCommitID() + ")"
}

// startIPC serves the local status / pair / llm HTTP API over the
// platform-local endpoint owned by agentredipc.
func (d *Daemon) startIPC(ctx context.Context) (*http.Server, error) {
	ln, err := agentredipc.Listen(d.opts.DataDir)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/local/pair", d.ipcPair)
	mux.HandleFunc("/local/status", d.ipcStatus)
	mux.HandleFunc("/local/llm", d.ipcLLM)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	//nolint:gosec // G118: shutdown must use a fresh ctx since the request ctx is already canceled
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
		_ = agentredipc.Cleanup(d.opts.DataDir)
	}()
	go func() { _ = srv.Serve(ln) }()
	return srv, nil
}

// SocketPath returns the daemon's platform-local IPC endpoint. The method name
// remains stable for existing status JSON and Unix callers.
func (d *Daemon) SocketPath() string {
	return agentredipc.Endpoint(d.opts.DataDir)
}

func (d *Daemon) ipcPair(w http.ResponseWriter, r *http.Request) {
	code, err := d.pairing.Generate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"code":       code,
		"ttlSeconds": d.state.Snapshot().Preferences.PairingCodeTTLSeconds,
		"listenURLs": lanURLs(d),
	})
}

func (d *Daemon) ipcStatus(w http.ResponseWriter, r *http.Request) {
	snap := d.state.Snapshot()
	// dbPath / dbSizeBytes:远端盒子上的 transcript 是永久档案,规格要求库文件的位置与
	// 体量在状态查询里看得见,用户据此自行判断何时该清理(见 Daemon.DBStat)。
	dbStat := d.DBStat()
	writeJSON(w, map[string]any{
		"pid":              os.Getpid(),
		"version":          BuildIdentity(),
		"daemonUUID":       snap.DaemonInstanceUUID,
		"listenURLs":       lanURLs(d),
		"socketPath":       d.SocketPath(),
		"pairedPeers":      summarizePeers(snap.PairedPeers),
		"activeSessions":   d.activeSessionCount(r.Context()),
		"llmProviderCount": len(snap.LLMProviders),
		"dbPath":           dbStat.Path,
		"dbSizeBytes":      dbStat.SizeBytes,
	})
}

// activeSessionCount 是「这台 daemon 此刻在为几条会话干活」,取自它自己记着的生命周期
// (一轮起手 running、轮末 idle、进程重启把非终态一律标 interrupted)—— 那是 daemon
// 上唯一知道这件事的地方。
//
// 读不出来返 0 并留一行日志:状态查询不该因为库忙而整个失败,但也不能让一个读失败
// 冒充「没有会话在跑」而不留痕迹。
func (d *Daemon) activeSessionCount(ctx context.Context) int64 {
	n, err := d.sessionStore.CountRunning(ctx)
	if err != nil {
		log.Printf("daemon.ipcStatus: count running sessions failed: %v", err)
		return 0
	}
	return n
}

func (d *Daemon) ipcLLM(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		llmH := handlers.NewLLMHandlers(d.state)
		res, _ := llmH.List(r.Context())
		writeJSON(w, res)
	case http.MethodPost:
		var p handlers.LLMUpsertParams
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		llmH := handlers.NewLLMHandlers(d.state)
		res, err := llmH.Upsert(r.Context(), p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	case http.MethodDelete:
		var p struct {
			ProviderKey string `json:"providerKey"`
		}
		_ = json.NewDecoder(r.Body).Decode(&p)
		llmH := handlers.NewLLMHandlers(d.state)
		res, err := llmH.Delete(r.Context(), handlers.LLMDeleteParams{ProviderKey: p.ProviderKey})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func lanURLs(d *Daemon) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.lan == nil {
		return nil
	}
	// AdvertiseURLs 而不是 URL():这份列表会被 `agentred pair` / `agentred status` 印出来
	// 让用户粘进桌面端,而通配监听下的 bind 地址("[::]:7456")粘过去指的是桌面端自己
	// 那台机器。找不到可路由地址时它给空列表,由 CLI 明确要求用户加 --host。
	return d.lan.AdvertiseURLs()
}

func summarizePeers(m map[string]state.PairedPeer) []map[string]any {
	out := make([]map[string]any, 0, len(m))
	for fp, p := range m {
		out = append(out, map[string]any{
			"fingerprint": fp,
			"deviceName":  p.DeviceName,
			"pairedAt":    p.PairedAt,
			"lastSeenAt":  p.LastSeenAt,
		})
	}
	return out
}
