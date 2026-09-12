# remote-devices

Desktop UI for pairing and managing agentred LAN devices.

## Components

| File | Purpose |
|---|---|
| `remote-devices-panel.tsx` | Settings → 远端 主面板，挂载 hook、调度对话框 |
| `device-row.tsx` | 单台 agentred 行卡片 |
| `device-action-menu.tsx` | 行右侧 `…` 菜单（Refresh / Rename / Edit TLS / Remove） |
| `agentred-onboarding.tsx` | 三步接入引导（安装 / 常驻 / 配对），页头「添加 agentred」召唤，页面上有设备行时默认收起、可收起。安装与常驻两段、命令与步骤条来自 `@agentre-hub/agentre-ui` 的引导域，与 agentre-server 同一份 |
| `device-pairing-form.tsx` | 配对表单：地址 + 6 位 code + name + Advanced TLS Trust（引导第 3 步的宿主） |
| `tls-trust-dialog.tsx` | 4 模式 radio：default / pin-cert / ca-bundle / skip-verify |
| `use-remote-devices.ts` | hook：list / mutate / 30 s 轮询 / window focus 重新拉 |
| `format.ts` | `relativeTime` / `deriveDeviceName` / `friendlyLastError` |

## Data flow

```
RemoteDevicesPanel
   │
   ├── useRemoteDevices ─── window.setInterval(30s) → RemoteDeviceRefresh(id) for each
   │                       window 'focus' event   → RemoteDeviceList
   │
   ├── AgentredOnboarding → DevicePairingForm
   │                          onSubmit({URL, code, name, tlsMode, tlsCertPEM})
   │                                                 ↓
   │                                           svc.Add (Go) → daemon auth.pair
   │
   ├── TLSTrustDialog ──── standalone: writes mode+pem into Add form
   │                       edit-row: svc.UpdateTLS → svc.Refresh
   │
   └── DeviceRow → DeviceActionMenu
                       ├── Refresh → svc.Refresh
                       ├── Rename  → RenameDialog → svc.Rename
                       ├── Edit TLS → opens TLSTrustDialog
                       └── Remove   → RemoveConfirmDialog → svc.Remove
```

## Manual smoke test (M7)

Requires mac with `agentre` + linux VM (or another machine) running `agentred`.

```bash
# On remote machine
agentred run --port 7456
agentred pair    # copy printed code

# On desktop
agentre   # open Settings → 远端 → 添加 agentred（零设备时引导已展开）
# Step 3: paste URL, paste 6-char code, leave TLS = Default, click Pair
# → row appears, status dot turns green within 30s

# Edit TLS → switch to Pin certificate → paste cert → Apply
# → row updates immediately (Refresh runs)

# Stop remote agentred
# → within 30s, row dot turns muted, last_error filled

# Restart remote agentred (same state.json)
# → within 30s, row dot turns green again

# Delete remote state.json + restart agentred
# → Refresh shows tofu_mismatch in red

# Remove device → DB row + keychain token disappear (verify via
# `sqlite3 ~/Library/Application\ Support/agentre/agentre.db
#  "SELECT name, url, status FROM paired_agentreds"`)
```
