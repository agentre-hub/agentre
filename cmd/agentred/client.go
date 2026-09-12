package main

import (
	"io"
	"net/http"

	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

// localClient dials the daemon's platform-local endpoint so CLI subcommands
// can use the same HTTP routes on Unix sockets and Windows named pipes.
func localClient() *http.Client {
	dir, _ := paths.AgentredDataDir()
	return &http.Client{Transport: &http.Transport{DialContext: localDialContext(dir)}}
}

func localGET(path string) ([]byte, error) {
	resp, err := localClient().Get("http://daemon" + path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}

func toAnySlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
