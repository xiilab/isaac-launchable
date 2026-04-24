# Probe results (2026-04-24)

> Probe ran against pod-1 (`instance=pod-1`) because pod-0 was Pending
> due to an unrelated hami-scheduler hostPort lock. Image is identical
> (isaac-launchable-vscode:6.0.0), so probe output is representative.
>
> Pod: `isaac-launchable-1-7965d4d9dc-vqzcc`, container `vscode`.
> Extscache probed:
> - `omni.kit.livestream.app-10.1.0+110.0.0.lx64.r.cp312`
> - `omni.kit.livestream.webrtc-10.1.2+110.0.0.lx64.r.cp312`
> - `omni.kit.livestream.core-10.0.0+110.0.0.lx64.r.cp312`

## Task 1: candidate port-range setting keys

### From extension.toml

From `omni.kit.livestream.app-10.1.0/config/extension.toml`, under
`[settings.exts."omni.kit.livestream.app".primaryStream]`:

- `publicIp` — "Fixed ip used to transport streaming media. Can help for connecting when server is behind a NAT."
- `signalPort = 49100` — "TCP port used for negotiating a connection. Must be unique for each stream, if not specified an attempt will be made to use an unoccupied port."
- `streamPort = 47998` — "UDP port used to transport streaming media. Must be unique for each stream, if not specified an attempt will be made to use an unoccupied port."
- `streamType = "webrtc"`
- `targetFps = 60`
- `allowDynamicResize`, `enableAudioCapture`, `enableEventTracing`, `enableOpenTelemetry`

Commented-out spectator example shows the same shape with `signalPort = 49200`,
`streamPort = 48000`.

From `omni.kit.livestream.webrtc-10.1.2/config/extension.toml`:

- Only `log.channels."omni.kit.livestream.streamsdk"` under `[settings]`. No port settings.

No literal `portRange`, `portMin`, `portMax`, `udpPortRange`, or `portBind`
appears in either extension.toml.

### From strings of omni.kit.livestream.webrtc plugin

From `libomni.kit.livestream.webrtc.plugin.so`:

- `sharedPort`
- `stunPort`
- `turnTransportPolicy`
- `Processed TURN details: %u URIs, transport policy: %s, relay location: %s`
- `Transport policy is DIRECT (UDP-only), not configuring TURN servers`
- `--/exts/omni.kit.livestream.app/primaryStream/allowDynamicResize=true` (referenced in a diagnostic message)

From `libNvStreamServer.so` (transport SDK bundled with the webrtc extension):

- `WebRtcUdpPort` (bare identifier, likely an NvSt-side setting name)
- `StreamerServerUdpControlPort`
- `StreamerServiceHttpPort`, `StreamerServiceHttpsPort`, `ApplevelServiceHttpsPort`
- `Binding to port range %d-%d` (format string)
- `Invalid port range: %hu-%hu` (format string)
- `Failed to bind to any port in range %hu-%hu after %zu attempts` (format string)
- `UDP RTP Source: no available port in range: %u-%u (Error: 0x%08X)` (format string)
- `UDP RTP Source: failed to bind to port: %u (Error: 0x%08X)`
- `UdpRtpSource creation for address %s has failed. Check if this port is in use by another process.`
- SDP-style x-nv- keys touching ports: `x-nv-general.clientBundlePort`, `x-nv-general.clientBundlePortUsage`, `x-nv-general.clientPorts.{audio,bundle,control,fallbackDynamic,localAddress,mic,session,useReserved,video}`, `x-nv-general.serverBundlePort`, `x-nv-general.nativeRtcOnBundlePort`

No literal `/exts/omni.kit.livestream.*/portRange/min` or equivalent string
exists in any of the webrtc-extension binaries. The identifiers containing
"port" at the Kit-settings layer are limited to `streamPort`, `signalPort`,
`sharedPort`, `stunPort`, and the NvSt-side `WebRtcUdpPort` /
`StreamerServerUdpControlPort` (which are accessed via internal NvSt config,
not a Kit `/exts/` path). A literal `Min`/`Max`/`Range` setting-key
identifier for the WebRTC UDP range was not found.

### From strings of omni.kit.livestream.app plugin

From `libomni.kit.livestream.app.plugin.so`:

- Setting prefixes (exact strings): `/exts/omni.kit.livestream.app/primaryStream`, `/exts/omni.kit.livestream.app/spectatorStream`
- Symbol-embedded identifiers: `kPrimaryStreamSettingPrefix`, `kSpectatorStreamSettingPrefix`, `kAovSpectatorStreamSettingPrefix`, `kPublicIpSetting`, `kSignalPortSetting`, `kStreamPortSetting`
- Runtime behavior identifiers: `nextAvailableSignalPort`, `nextAvailableStreamPort`, `getDesiredSpectatorStreamCount`, `ensurePrimaryStreamServer`, `createSpectatorStreamServer`, `ensureSpectatorStreamServers`, `primaryStreamSettings`, `spectatorStreamSettingsArray`, `primaryStreamType`, `spectatorStreamType`, `m_primaryStreamServer`, `m_spectatorStreamServers`
- Consumer symbols: `SettingsHelper::getPrimaryStreamSettingValue<bool|int|short unsigned int|omni::string>`, `SettingsHelper::setPrimaryStreamSettingValue<short unsigned int>`, `SettingsHelper::getSpectatorStreamSettingValue<...>`

From `libomni.kit.livestream.core.plugin.so`:

- Symbol identifiers: `acquireSignalPort`, `acquireStreamPort`, `releaseSignalPort`, `releaseStreamPort`, `m_occupiedSignalPorts`, `m_occupiedStreamPorts`, `IServerFactory::{acquire,release}{Signal,Stream}Port`

### Best candidate to try first in Task 3

`/exts/omni.kit.livestream.app/primaryStream/streamPort`

Rationale: this is the only observed Kit-level setting key that controls the
UDP base port for the primary stream, and the NvSt layer (via
`Binding to port range %d-%d` / `no available port in range: %u-%u`) scans
forward from that base, allocating subsequent UDP ports as ephemeral bindings
for the session. No literal `portRange`/`portMin`/`portMax` setting key was
found in any probed binary, so constraining the UDP footprint is most likely
achieved by pinning `streamPort` (and `signalPort` for TCP) and letting the
NvSt allocator walk upward from there. Task 3 should empirically test how
many consecutive UDP ports NvSt consumes starting from a pinned `streamPort`.

## Task 3 outcome
(filled after Task 3)

## Task 3 outcome (2026-04-24 ~10:15)

**Verdict: FAIL** — Track C 폐기.

### 측정 방법
pod-0 `hostNetwork=true`, sidecar ports 제거. `--kit_args` 에:
```
--/exts/omni.kit.livestream.app/primaryStream/publicIp=10.61.3.74
--/exts/omni.kit.livestream.app/primaryStream/signalPort=49100
--/exts/omni.kit.livestream.app/primaryStream/streamPort=30998
--merge-config=/isaac-sim/config/open_endpoint.toml
```
`Simulation App Startup Complete` 직후 `ss -uln` 캡처.

### 결과

Kit 기동 후 새로 bind 된 UDP 포트 (baseline 제외, IPv4):

```
0.0.0.0:37879
0.0.0.0:38674
0.0.0.0:47750
0.0.0.0:49770
0.0.0.0:53957
```

**30998 은 bind 되지 않음.** Kit이 streamPort 설정을 무시하고 OS ephemeral range (≈32768–60999) 에서 random 선택.

### 함의

1. Kit CLI `--/exts/omni.kit.livestream.app/primaryStream/streamPort=<N>` 은 ICE candidate advertise 값일 뿐, **실제 UDP bind port 와 무관**.
2. Kit 공개 설정 중 UDP bind range 를 제약할 수 있는 key 는 없음 (Task 1 에서 이미 확인됨). NvSt 내부의 `WebRtcUdpPort`, `StreamerServerUdpControlPort` 는 Kit `--/...` CLI 로 접근 불가한 NvSt-native 설정.
3. 따라서 Track C (portRange + hostPort 100-port window) 는 실현 불가. Decision gate per plan 발동 → Track D 로 전환.

### Track D 전환 시 재검증 필요 사항

- Kit `omni.kit.livestream.webrtc` 가 iceServers / iceTransportPolicy 를 respect 하는가? 본 플랜의 Spec Q2.
- NvSt 의 `Processed TURN details: %u URIs` 로그가 찍히는 조건? → Kit 로그에서 TURN 협상 메시지 확인 필요.
- coturn (k8s/base/turn.yaml) 의 shared secret / realm / lt-cred-mech 설정 상태.
