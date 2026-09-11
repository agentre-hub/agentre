# remote-devices

Desktop UI for pairing and managing agentred LAN devices.

## Components

| File | Purpose |
|---|---|
| `remote-devices-panel.tsx` | Settings → 远端 主面板，挂载 hook、调度对话框 |
| `device-row.tsx` | 单台 agentred 行卡片 |
| `device-row-version.tsx` | 副行的版本呈现：版本文字（决策 5 开发构建）、可升级/协议不匹配徽标（决策 17）、协议不匹配的强提示与命令卡（决策 18） |
| `device-row-upgrade.tsx` | 行卡片下方/菜单里的「一键升级」呈现：菜单项文案与可用性（决策 5/20/21）、准备中/升级中/成功/超时的一句话反馈、活跃轮次的二次确认（决策 8/21） |
| `desktop-device-row.tsx` | 账号设备清单里 kind=desktop 行的展开区：会话列表 / 「Agentre 未运行」 |
| `device-action-menu.tsx` | 行右侧 `…` 菜单（Refresh / Rename / Edit TLS / Remove） |
| `agentred-onboarding.tsx` | 三步接入引导（安装 / 常驻 / 配对），页头「添加 agentred」召唤，页面上有设备行时默认收起、可收起。安装与常驻两段、命令与步骤条来自 `@agentre-hub/agentre-ui` 的引导域，与 agentre-server 同一份 |
| `device-pairing-form.tsx` | 配对表单：地址 + 6 位 code + name + Advanced TLS Trust（引导第 3 步的宿主） |
| `tls-trust-dialog.tsx` | 4 模式 radio：default / pin-cert / ca-bundle / skip-verify |
| `device-port-forward.tsx` | 设备行下的「端口转发」子块：把共享包 `PortForwardSection` 接到 PortForward* 绑定 |
| `device-providers-sync.tsx` | 设备行展开时的 provider 同步子面板 |
| `login-dialog.tsx` | 账号登录对话框（设备流：code / 倒计时 / 打开浏览器） |
| `mention-items.ts` | 设备面板行 → `@` 菜单设备清单的投影（本机置顶、指纹为唯一身份） |
| `agentred-version.ts` | agentred 版本比较与升级判定（semver 解析、预发布排序） |
| `use-remote-devices.ts` | hook：list / mutate / `remote.device.state` 事件推送 / window focus 重新拉 |
| `use-device-mentions.ts` | `@` 菜单设备清单取数（走 device-list-store，复用面板同一份合并规则） |
| `use-device-upgrade.ts` | 桌面端取数适配：把共享包 `useAgentredUpgrade` 接到 Wails 绑定 |
| `use-latest-agentred-version.ts` | 「桌面端已知的最新 agentred 版本」：update store 结果 + 本机构建标识 |
| `use-server-login.ts` | 账号登录态 hook（ServerGetState / StartLogin / Poll / Logout + `server.state` 事件） |
| `format.ts` | `relativeTime` / `deriveDeviceName` / `friendlyLastError` |

## Data flow

```
RemoteDevicesPanel
   │
   ├── useRemoteDevices ─── EventsOn('remote.device.state') → row state update
   │                       window 'focus' event            → RemoteDeviceList
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
# → row appears, status dot turns green

# Edit TLS → switch to Pin certificate → paste cert → Apply
# → row updates immediately (Refresh runs)

# Stop remote agentred
# → row dot turns muted, last_error filled

# Restart remote agentred (same state.json)
# → row dot turns green again

# Delete remote state.json + restart agentred
# → Refresh shows tofu_mismatch in red

# Remove device → DB row + keychain token disappear (verify via
# `sqlite3 ~/Library/Application\ Support/agentre/agentre.db
#  "SELECT name, url, status FROM paired_agentreds"`)
```
