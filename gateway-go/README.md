# gateway-go — Pion SFU gateway

WebRTC SFU sidecar that mediates between Isaac Sim's Kit livestream
(`omni.kit.livestream.app`) and the browser. Replaces the Node.js
signaling-only proxy in `../gateway/` for pod-0 to enable media relay
through coturn (`turn:10.61.3.74:3478`) without requiring `hostNetwork`.

## Why this exists

- Kit's `NvSt` layer ignores `iceServers`, so TURN relay cannot be
  forced from inside Kit.
- Pod-network UDP candidates (pod IP + ephemeral port) are not
  reachable from the browser.
- Therefore the gateway must become an actual WebRTC peer on both
  sides: pod-loopback to Kit (no relay needed, ephemeral UDP reachable
  over loopback), TURN-relay-only to the browser.

## Architecture

```
browser ─(NVST WS + TURN relay media)─ gateway-go ─(NVST WS + loopback media)─ Kit
                                          │
                                    ┌─────┴──────┐
                                    │ upstream   │  Pion peer (answerer to Kit)
                                    │ + relay    │  track-forwarding RTP
                                    │ downstream │  Pion peer (offerer to browser,
                                    └────────────┘  iceTransportPolicy=relay)
```

Package layout:
- `cmd/gateway` — entry point, env config, HTTP mux (`/`, `/healthz`)
- `internal/config` — env var parsing with validation
- `internal/nvst` — NVIDIA signaling envelope parser (offer/answer/candidate + verbatim passthrough for unknown types)
- `internal/proxy` — browser↔gateway↔Kit WS proxy with per-direction hooks
- `internal/upstream` — gateway's Pion peer for Kit (loopback)
- `internal/downstream` — gateway's Pion peer for browser (TURN relay)
- `internal/relay` — RTP track pass-through (SFU pattern, no re-encoding)
- `internal/session` — wires everything together per browser connection

## Environment variables

| Name | Required | Example | Purpose |
|------|----------|---------|---------|
| `KIT_SIGNAL_URL` | yes | `ws://127.0.0.1:49100` | Kit's NVST signaling (loopback, no `/sign_in` suffix — proxy appends per-request path) |
| `TURN_URI` | yes | `turn:10.61.3.74:3478` | coturn |
| `TURN_USERNAME` | yes | `isaac` | coturn shared-secret username |
| `TURN_CREDENTIAL` | yes | (from secret) | coturn credential |
| `LISTEN_ADDR` | no | `:9000` | bind addr (default `:9000`) |

## Build

```bash
cd gateway-go
go test ./...
go build ./cmd/gateway
```

Container build (on ws-node074):
```bash
docker build -t 10.61.3.124:30002/library/isaac-launchable-gateway-go:dev .
docker push 10.61.3.124:30002/library/isaac-launchable-gateway-go:dev
```

## Rollback

The Node.js `../gateway/` remains deployed in `k8s/isaac-sim/deployment-0.yaml`'s history. Swap the image back to
`10.61.3.124:30002/library/isaac-launchable-gateway:dev` and rollout restart.

## Limitations

- Only Kit-initiates-offer pattern implemented. Browser-initiates pattern logs a warning and ignores.
- No RTCP PLI/FIR bridging yet — initial keyframe relies on Kit's keyframe cadence.
- No session reconnection logic; browser reload is required on transient failure.
