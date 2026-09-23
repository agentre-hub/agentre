package ctlcmd

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

const usageText = `agrctl — Agentre companion CLI: manage and drive the Agentre desktop on this machine

Usage:
  agrctl list <resource> [filters] [-o json]    list resources as a table (first column: ID)
  agrctl get <resource> <ref>                   print one resource as JSON
  agrctl create <resource> [flags]              create a resource
  agrctl update <resource> <ref> [flags]        change only the flags given
  agrctl delete <resource> <ref> [flags]        delete a resource
  agrctl help [<resource> [<backend type>]]     flags, --config fields, secrets and examples
  agrctl send --agent <name> [flags] <text...>  dispatch a task to an agent
  agrctl acp --agent <name>                     run as an ACP v1 agent over stdio

Resources: agent, department (dept), project (proj), provider, model, backend.
<ref> is a numeric id, a name, or a parent/child path (projects, departments);
a model is <provider>/<model id>. Flags that name another resource
(--department, --parent, --backend, --provider, --lead, …) take the same forms.

Writes from an Agentre session wait for approval in that session; writes from a
program without a terminal wait for approval in the desktop.

Exit codes: 0 ok · 1 failed, rejected or executor unreachable · 2 usage error
(unknown command or flag, missing value, ambiguous ref) · 3 needs a human
(a secret must be typed in a terminal: NEEDS TTY).`

const sendUsage = `agrctl send --agent <name> [flags] <task text...>

  --agent <name>     target agent by name
  --agent-id <id>    target agent by id (overrides --agent)
  --project <id>     project id to run in (0 = free session)
  --wait             block until the turn finishes, then print its final text
  --isolated         one-shot isolated session (not shown in the sidebar)
  --endpoint <url>   control endpoint URL   (env AGENTRE_CTL_ENDPOINT)
  --token <token>    control token          (env AGENTRE_CTL_TOKEN)`

// runHelp：`help [资源 [后端类型]]`。纯客户端，不连接任何执行者。
func runHelp(args []string, w io.Writer) error {
	switch len(args) {
	case 0:
		_, _ = fmt.Fprintln(w, usageText)
		return nil
	case 1, 2:
	default:
		return usageErrorf("help takes at most a resource and a backend type")
	}
	if args[0] == "send" && len(args) == 1 {
		_, _ = fmt.Fprintln(w, sendUsage)
		return nil
	}
	spec := lookupKind(args[0])
	if spec == nil {
		return usageErrorf("unknown resource %q (one of: %s)", args[0], kindNames())
	}
	if len(args) == 2 {
		if spec != kindBackend {
			return usageErrorf("only backend has per-type help")
		}
		t := lookupBackendType(args[1])
		if t == nil {
			return usageErrorf("unknown backend type %q (one of: %s)", args[1], backendTypeNames())
		}
		writeBackendTypeHelp(w, t)
		return nil
	}
	writeKindHelp(w, spec)
	return nil
}

func writeKindHelp(w io.Writer, spec *kindSpec) {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Resource: %s\n\nCommands:\n", spec.name)
	ref := "<ref>"
	if spec == kindModel {
		ref = "<provider>/<model id>"
	}
	filters := ""
	for _, f := range spec.filters {
		filters += " [--" + f.name + " " + f.value + "]"
	}
	_, _ = fmt.Fprintf(&b, "  agrctl list %s%s [-o json]\n", spec.plural, filters)
	_, _ = fmt.Fprintf(&b, "  agrctl get %s %s\n", spec.name, ref)
	req := ""
	for _, r := range spec.required {
		if d := findDef(spec.createFlags, r); d != nil {
			req += " --" + r + " " + d.value
		}
	}
	_, _ = fmt.Fprintf(&b, "  agrctl create %s%s [flags]\n", spec.name, req)
	_, _ = fmt.Fprintf(&b, "  agrctl update %s %s [flags]    (only the flags given change)\n", spec.name, ref)
	del := ""
	for _, f := range spec.deleteFlags {
		del += " [--" + f.name + "]"
	}
	_, _ = fmt.Fprintf(&b, "  agrctl delete %s %s%s\n", spec.name, ref, del)

	b.WriteString("\nFlags (create / update):\n")
	writeFlagTable(&b, mergeFlags(spec.createFlags, spec.updateFlags), func(d *flagDef) string {
		inCreate, inUpdate := findDef(spec.createFlags, d.name) != nil, findDef(spec.updateFlags, d.name) != nil
		switch {
		case inCreate && !inUpdate && !strings.Contains(d.usage, "create only"):
			return " (create only)"
		case inUpdate && !inCreate:
			return " (update only)"
		}
		return ""
	})
	if len(spec.deleteFlags) > 0 {
		b.WriteString("\nFlags (delete):\n")
		writeFlagTable(&b, spec.deleteFlags, nil)
	}
	if len(spec.filters) > 0 {
		b.WriteString("\nFilters (list):\n")
		writeFlagTable(&b, spec.filters, nil)
	}
	if spec == kindBackend {
		_, _ = fmt.Fprintf(&b, "\nTypes: %s\n  --config fields depend on the type: agrctl help backend <type>\n", backendTypeNames())
	}
	b.WriteString("\nSecrets:\n")
	if spec.secretHelp != "" {
		b.WriteString("  " + strings.ReplaceAll(spec.secretHelp, "\n", "\n  ") + "\n")
	} else if spec == kindBackend {
		b.WriteString("  --token (openclaw only), see agrctl help backend openclaw\n")
	} else {
		b.WriteString("  none\n")
	}
	b.WriteString("\nExamples:\n")
	for _, e := range spec.examples {
		b.WriteString("  " + e + "\n")
	}
	_, _ = io.WriteString(w, b.String())
}

// mergeFlags 合并 create 与 update 的 flag，保持首次出现的顺序。
func mergeFlags(a, b []*flagDef) []*flagDef {
	out := append([]*flagDef{}, a...)
	for _, d := range b {
		if findDef(out, d.name) == nil {
			out = append(out, d)
		}
	}
	return out
}

// writeFlagTable 写一张 flag 表；note 可为 nil，否则给每个 flag 追加一段说明。
func writeFlagTable(b *strings.Builder, defs []*flagDef, note func(*flagDef) string) {
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	for _, d := range defs {
		name := "--" + d.name
		switch {
		case d.secret:
			name += "[=<value>]"
		case d.value != "":
			name += " " + d.value
		}
		usage := d.usage
		if note != nil {
			usage += note(d)
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", name, usage)
	}
	_ = tw.Flush()
}

func writeBackendTypeHelp(w io.Writer, t *backendType) {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Backend type: %s — %s\n\n", t.name, t.summary)
	b.WriteString("Flags: see agrctl help backend (--name, --device, --provider, --model,\n" +
		"       --reasoning-effort, --env KEY=VAL, --config, --config-file)\n\n")
	b.WriteString("--config fields:\n")
	if len(t.config) == 0 {
		b.WriteString("  none (any --config key is rejected)\n")
	} else {
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, f := range t.config {
			_, _ = fmt.Fprintf(tw, "  %s\t%s\n", f.key, f.values)
		}
		_ = tw.Flush()
	}
	b.WriteString("\nSecrets:\n")
	if t.token {
		b.WriteString("  --token        bare: typed in without echo (needs a terminal).\n" +
			"                 --token=<value> also works, but the token stays in your shell history.\n" +
			"  get / list show only its state: set / unset / unknown. unknown means the device the\n" +
			"  backend is bound to cannot be asked right now (offline, or managed through the server).\n")
	} else {
		b.WriteString("  none\n")
	}
	b.WriteString("\nExample:\n")
	switch t.name {
	case "openclaw":
		b.WriteString(`  agrctl create backend --type openclaw --name claw --config '{"openclawGatewayUrl":"ws://127.0.0.1:18789","openclawSessionMode":"per-agentre-session"}' --token` + "\n")
	case "codex":
		b.WriteString(`  agrctl create backend --type codex --name codex-remote --device build-box --config '{"sandbox":"workspace-write","approval":"on-request"}'` + "\n")
	default:
		_, _ = fmt.Fprintf(&b, "  agrctl create backend --type %s --name my-%s\n", t.name, t.name)
	}
	_, _ = io.WriteString(w, b.String())
}
