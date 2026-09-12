package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newPairCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pair",
		Short: "Mint a one-shot pairing code + advertise listen URLs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body, err := localGET("/local/pair")
			if err != nil {
				return err
			}
			var v map[string]any
			if err := json.Unmarshal(body, &v); err != nil {
				return err
			}
			printPair(cmd.OutOrStdout(), v)
			return nil
		},
	}
}

// printPair 渲染 /local/pair 的应答。
//
// listenURLs 是空的意味着 daemon 监听在通配地址上、却找不到任何对端拨得通的本机地址
// (见 rpc.LANServer.AdvertiseURLs —— 它宁可什么都不报,也不把 "[::]" 那种 bind 地址
// 交出去,因为粘到桌面端只会让桌面端拨它自己)。这时候印一句 "use any of:" 加一片空白
// 等于把用户扔在半路:他手里有配对码,却不知道该粘什么、更不会想到要 --host。
func printPair(w io.Writer, v map[string]any) {
	_, _ = fmt.Fprintf(w, "Pairing code: %v\n", v["code"])
	_, _ = fmt.Fprintf(w, "Expires in %v seconds.\n", v["ttlSeconds"])
	urls := toAnySlice(v["listenURLs"])
	if len(urls) == 0 {
		_, _ = fmt.Fprintln(w, "No LAN address to advertise: this daemon has no address another machine can reach.")
		_, _ = fmt.Fprintln(w, "Restart it with an explicit one — `agentred run --host <this machine's LAN IP>`")
		_, _ = fmt.Fprintln(w, "(the value is remembered, including for `agentred service start`) — then pair again.")
		return
	}
	_, _ = fmt.Fprintln(w, "On desktop, use any of:")
	for _, u := range urls {
		_, _ = fmt.Fprintf(w, "  %v\n", u)
	}
}
