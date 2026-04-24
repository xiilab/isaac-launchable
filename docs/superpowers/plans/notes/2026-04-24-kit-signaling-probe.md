# Kit signaling protocol probe (2026-04-24)

## Scope
Before implementing the Pion Gateway (Plan Task 2 onward), verify that Kit's `omni.kit.livestream.app` WebSocket signaling at `:49100/sign_in` is a standard-enough protocol to reproduce from a Go Pion client.

## URL shape
```
ws://127.0.0.1:49100/sign_in?peer_id=<id>&version=2&reconnect=1
```

Extracted from `@nvidia/omniverse-webrtc-streaming-library` v5.16.3 (minified dist): `sign_in?peer_id=${...}&${signalingQuery}`.

## Probe attempts

### Attempt 1 — arbitrary peer id + send "start" + "config"
```python
peer_id = "probe-gw-1"
await ws.send({"type":"start","peer_id":"probe-gw-1"})
```

Result: `websockets.exceptions.ConnectionClosedError: no close frame received or sent` immediately after send. The server refused the frame before even acknowledging.

### Attempt 2 — browser-like numeric peer id, passive receive
```python
peer_id = "peer-7230488145"
# no outgoing message, just read
```

First (and only) frame received from server:

```
{"error":"peerRemoved"}
```

then connection closed.

## Findings

1. **Endpoint is reachable** (`49100/TCP` accepts WebSocket upgrade).
2. **Server enforces a handshake we did not perform.** It either expects:
   - a specific `signalingQuery` we didn't supply (cookie, token, session id), OR
   - an initial control message (not `{"type":"start"}`) that we didn't send, OR
   - an out-of-band registration before `sign_in` (the library may POST something to another endpoint first).
3. The library source is **minified JS** in `node_modules/@nvidia/omniverse-webrtc-streaming-library/dist/omniverse-webrtc-streaming-library.js`. Readable string extraction yields transition enums (`"active":"passive"`, `"resume":"start"`, `"relay":"all"`, `"completed":"gathering"`, etc.) but no full protocol state machine without deobfuscation.
4. Browser console previously showed these human-readable sequence markers:
   ```
   Success: Starting stream.
   Session started successfully
   Stream Ready
   ```
   These are NVST library status messages, not raw server frames.

## Verdict against Plan's decision gate

> "Ability to replicate in Pion: yes/no?"

**Answer: NO — not without multi-day reverse engineering.**

The Plan's Phase 1 gate said: _"If 'Ability to replicate in Pion' is NO, stop this plan. Escalate to redesign."_

## Recommended pivot

Rather than reverse-engineer NVST's WebSocket signaling, **reuse the NVIDIA client library itself** by running it server-side in the Gateway:

- Implement the Gateway in **Node.js** (instead of Go Pion).
- `import { AppStreamer } from '@nvidia/omniverse-webrtc-streaming-library'` as the **upstream** client.
- Use a standard WebRTC peer (e.g., `wrtc` or `node-datachannel`) for the **downstream** connection with `iceTransportPolicy: 'relay'`.
- This assumes the NVIDIA library works under Node.js (needs polyfill for `window.RTCPeerConnection` etc.). Alternative: run a headless-Chromium Gateway that imports the library natively.

### Confidence
- Pion Gateway (original plan): low — blocked by protocol opacity.
- Node.js Gateway with NVIDIA library: medium — depends on library's Node compatibility.
- Headless Chromium Gateway (`puppeteer`-style): high but heavy (adds ~200 MB image, more latency).

## Artifacts to commit
- This file.
- Do NOT start Go implementation (Plan Task 2+) until a new plan is written for the chosen pivot.

## Next action for the controller
Surface findings to the user, confirm which Gateway runtime to pursue, then rewrite the plan (keep phases 3-6 unchanged; only phase 2 implementation layer shifts).
