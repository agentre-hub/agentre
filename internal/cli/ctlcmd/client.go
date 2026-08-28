package ctlcmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/ctlendpoint"
	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

// endpoint 已解析的控制端点：base URL + bearer token。
type endpoint struct {
	base  string
	token string
}

// resolveEndpoint 按优先级解析控制端点：flag > 环境变量 > AppDataDir 握手文件。
func resolveEndpoint(flagURL, flagToken string, lookupEnv func(string) (string, bool)) (endpoint, error) {
	ep := endpoint{base: strings.TrimRight(flagURL, "/"), token: flagToken}
	if ep.base == "" {
		if v, ok := lookupEnv("AGENTRE_CTL_ENDPOINT"); ok {
			ep.base = strings.TrimRight(v, "/")
		}
	}
	if ep.token == "" {
		if v, ok := lookupEnv("AGENTRE_CTL_TOKEN"); ok {
			ep.token = v
		}
	}
	if ep.base == "" || ep.token == "" {
		if dir, derr := paths.AppDataDir(); derr == nil {
			fe, rerr := ctlendpoint.Read(dir)
			switch {
			case rerr == nil:
				if ep.base == "" {
					ep.base = strings.TrimRight(fe.URL, "/")
				}
				if ep.token == "" {
					ep.token = fe.Token
				}
			case errors.Is(rerr, os.ErrNotExist):
				return endpoint{}, errors.New("agentre desktop control endpoint not found — is the desktop app running?")
			default:
				return endpoint{}, fmt.Errorf("read control endpoint: %w", rerr)
			}
		}
	}
	if ep.base == "" || ep.token == "" {
		return endpoint{}, errors.New("control endpoint not configured — is the desktop app running?")
	}
	return ep, nil
}

func (e endpoint) get(path string, out any) error {
	return e.do(http.MethodGet, path, nil, out)
}

func (e endpoint) post(path string, body any, out any) error {
	return e.do(http.MethodPost, path, body, out)
}

func (e endpoint) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect to desktop: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("control error (%d): %s", resp.StatusCode, serverErrMsg(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// serverErrMsg 从 {"error": "..."} 里取消息，取不到就回原始文本。
func serverErrMsg(raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(raw))
}
