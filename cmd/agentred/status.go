package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print daemon state via the local unix socket",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := localGET("/local/status")
			if err != nil {
				return fmt.Errorf("daemon not running? %w", err)
			}
			var v map[string]any
			if err := json.Unmarshal(body, &v); err != nil {
				return err
			}
			printStatus(cmd.OutOrStdout(), v)
			return nil
		},
	}
}

// printStatus 渲染 /local/status 的应答。
//
// 库文件那一行不是可选装饰:远端盒子上的 transcript 是永久落盘的档案,规格要求库文件的
// 位置与体量在 daemon 状态查询里看得见,用户据此自行判断何时该清理。daemon 没报 dbPath
// (老版本)时整行不印 —— 印一行空路径会让人以为库文件丢了。
func printStatus(w io.Writer, v map[string]any) {
	_, _ = fmt.Fprintf(w, "Daemon running, pid %v\n", v["pid"])
	printStatusDetails(w, v)
}

func printServiceDaemonStatus(w io.Writer, v map[string]any) {
	_, _ = fmt.Fprintln(w, "Daemon running")
	_, _ = fmt.Fprintf(w, "PID: %v\n", v["pid"])
	printStatusDetails(w, v)
}

func printStatusDetails(w io.Writer, v map[string]any) {
	if version, ok := v["version"].(string); ok && version != "" {
		_, _ = fmt.Fprintf(w, "Version: %s\n", version)
	}
	_, _ = fmt.Fprintln(w, "Listening on:")
	for _, u := range toAnySlice(v["listenURLs"]) {
		_, _ = fmt.Fprintf(w, "  %v\n", u)
	}
	_, _ = fmt.Fprintf(w, "Paired devices: %d\n", len(toAnySlice(v["pairedPeers"])))
	state := "disconnected"
	if connected, _ := v["relayConnected"].(bool); connected {
		state = "connected"
	}
	_, _ = fmt.Fprintf(w, "Relay: %s\n", state)
	count, _ := v["clientConnectionCount"].(float64)
	_, _ = fmt.Fprintf(w, "Client connections: %.0f\n", count)
	_, _ = fmt.Fprintf(w, "Active sessions: %v\n", v["activeSessions"])
	_, _ = fmt.Fprintf(w, "LLM providers: %v\n", v["llmProviderCount"])
	if path, ok := v["dbPath"].(string); ok && path != "" {
		size, _ := v["dbSizeBytes"].(float64)
		_, _ = fmt.Fprintf(w, "Database: %s (%s)\n", path, humanBytes(int64(size)))
	}
}

// humanBytes 把字节数印成人读得懂的量级。裸字节数在 GB 级上判断不了「该不该清理」,
// 而这一行存在的全部理由就是让用户做那个判断。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	// 封顶在 TB:int64 的字节数到不了下一级,继续进位只会让 div 溢出。
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
