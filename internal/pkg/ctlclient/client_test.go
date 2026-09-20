package ctlclient

import (
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/ctlendpoint"
)

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestResolve_FlagWinsOverEnv(t *testing.T) {
	got, err := Resolve("http://flag", "flag-token", envOf(map[string]string{
		"AGENTRE_CTL_ENDPOINT": "http://env",
		"AGENTRE_CTL_TOKEN":    "env-token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "http://flag" || got.Token != "flag-token" {
		t.Fatalf("got %+v, want flag values", got)
	}
}

func TestResolve_EnvUsedWhenNoFlag(t *testing.T) {
	got, err := Resolve("", "", envOf(map[string]string{
		"AGENTRE_CTL_ENDPOINT": "http://env/", // trailing slash trimmed
		"AGENTRE_CTL_TOKEN":    "env-token",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "http://env" || got.Token != "env-token" {
		t.Fatalf("got %+v, want env values", got)
	}
}

func TestResolve_FallsBackToHandshakeFile(t *testing.T) {
	dir := t.TempDir()
	if err := ctlendpoint.Write(dir, ctlendpoint.Endpoint{URL: "http://handshake", Token: "hs-token"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTRE_DATA_DIR", dir)

	got, err := Resolve("", "", envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "http://handshake" || got.Token != "hs-token" {
		t.Fatalf("got %+v, want handshake values", got)
	}
}

func TestResolve_MissingEverythingIsReadableError(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", t.TempDir())
	_, err := Resolve("", "", envOf(nil))
	if err == nil {
		t.Fatal("want an error when no endpoint is configured")
	}
}
