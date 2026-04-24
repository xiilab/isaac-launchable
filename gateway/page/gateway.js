// gateway/page/gateway.js
// Page-side logic for the isaac-launchable WebRTC gateway. Runs inside
// a headless Chromium controlled by gateway/main.js via Puppeteer.
//
// Responsibilities:
//   1. Construct an UPSTREAM WebRTC peer against Kit livestream using
//      the NVIDIA @nvidia/omniverse-webrtc-streaming-library.
//   2. Construct a DOWNSTREAM standard RTCPeerConnection with
//      iceTransportPolicy=relay + coturn iceServers, and forward the
//      upstream video track to it.
//   3. Bridge SDP/ICE between the downstream peer and the browser via
//      Node-exposed functions (window.gwPageSendAnswer, etc.) added by
//      Puppeteer's page.exposeFunction in gateway/main.js.

// STUN-free upstream peer: the NVIDIA library defaults to public STUN
// (stun.l.google.com:19302 etc.) which is unreachable from the pod network.
// Upstream connects to Kit via pod-loopback (127.0.0.1), so host candidates
// alone are sufficient. Override window.RTCPeerConnection to strip iceServers
// for library-created peers. Downstream peers (our code) set
// __bypassGatewayPatch: true in the config so TURN-relay settings survive.
;(function() {
  const OrigPC = window.RTCPeerConnection;
  const Patched = function(cfg) {
    const c = cfg || {};
    if (c.__bypassGatewayPatch) {
      const { __bypassGatewayPatch, ...rest } = c;
      return new OrigPC(rest);
    }
    const stripped = {
      ...c,
      iceServers: [],
      iceTransportPolicy: "all",
    };
    return new OrigPC(stripped);
  };
  Patched.prototype = OrigPC.prototype;
  Object.setPrototypeOf(Patched, OrigPC);
  window.RTCPeerConnection = Patched;
  console.log("[gateway] installed RTCPeerConnection monkey-patch (STUN-free for upstream)");
})();

const cfg = readConfigFromQueryString();

const state = {
  status: "initializing",
  upstreamPeer: null,
  downstreamPeer: null,
  upstreamTrack: null,
  onUpstreamTerminated: null,
};

function readConfigFromQueryString() {
  const p = new URLSearchParams(window.location.search);
  return {
    kitSignalUrl: p.get("kit_signal_url") || "ws://127.0.0.1:49100/sign_in",
    turnUri:      p.get("turn_uri")       || "",
    turnUsername: p.get("turn_username")  || "",
    turnCred:     p.get("turn_cred")      || "",
  };
}

function setStatus(s) {
  state.status = s;
  const el = document.getElementById("status");
  if (el) el.textContent = s;
  console.log("[gateway]", s);
}

// Parse cfg.kitSignalUrl into AppStreamer's signalingServer/Port/Path.
// The library internally constructs the WS URL as
//   ${proto}://${signalingServer}:${signalingPort}${signalingPath}
// where signalingPath defaults to "/sign_in".
function parseKitSignalUrl(urlStr) {
  const u = new URL(urlStr);
  const port = u.port ? Number(u.port) : (u.protocol === "wss:" ? 443 : 80);
  // The NVIDIA library's default signalingPath is "/sign_in" and applied
  // internally. Setting it explicitly (even to the same "/sign_in") can
  // produce duplicated paths like "/sign_in/sign_in" depending on library
  // version. Only pass a path when it is non-default.
  const rawPath = u.pathname || "";
  const path = (rawPath === "" || rawPath === "/" || rawPath === "/sign_in") ? "" : rawPath;
  return {
    host: u.hostname,
    port,
    path,           // "" means "let the library use its default"
    secure: u.protocol === "wss:",
  };
}

// ── UPSTREAM ──────────────────────────────────────────────────
// Uses NVIDIA Omniverse WebRTC streaming library (UMD global
// OVWebStreamingLibrary). The library binds the received video track
// to an <video> element specified by videoElementId, then we capture
// its MediaStream via the element's srcObject for forwarding.
async function startUpstream() {
  const lib = window.OVWebStreamingLibrary;
  if (!lib || !lib.AppStreamer) {
    throw new Error("OVWebStreamingLibrary not loaded");
  }

  // The library binds to a <video> element by id. Create it if the
  // HTML shell does not already provide one — the element is never
  // displayed (headless Chromium), it only serves as a handle to the
  // MediaStream so we can forward its track downstream.
  let videoEl = document.getElementById("remote-video");
  if (!videoEl) {
    videoEl = document.createElement("video");
    videoEl.id = "remote-video";
    videoEl.autoplay = true;
    videoEl.muted = true;
    videoEl.playsInline = true;
    videoEl.style.display = "none";
    document.body.appendChild(videoEl);
  }

  const sig = parseKitSignalUrl(cfg.kitSignalUrl);

  // When the library attaches the MediaStream to the <video> element,
  // .srcObject becomes a MediaStream we can pull the track from. The
  // event sequence is: loadedmetadata -> playing. We listen on both
  // and poll srcObject defensively because the sample shows the library
  // manages playback internally.
  const captureTrack = () => {
    if (state.upstreamTrack) return;
    const stream = videoEl.srcObject;
    if (!stream || typeof stream.getVideoTracks !== "function") return;
    const tracks = stream.getVideoTracks();
    if (tracks.length === 0) return;
    state.upstreamTrack = tracks[0];
    console.log("[gateway] upstream video track captured:", state.upstreamTrack.id);
    maybeStartDownstream();
  };

  videoEl.addEventListener("loadedmetadata", captureTrack);
  videoEl.addEventListener("playing", captureTrack);

  const streamConfig = {
    videoElementId:  "remote-video",
    signalingServer: sig.host,
    signalingPort:   sig.port,
    ...(sig.path ? { signalingPath: sig.path } : {}),
    // For the Kit loopback case, media flows over the same WS/host.
    mediaServer:     sig.host,
    mediaPort:       sig.port,
    forceWSS:        sig.secure,
    width:           1920,
    height:          1080,
    fps:             60,
    onStart: (message) => {
      console.log("[gateway] upstream onStart:", message);
      const action = message && message.action;
      const status = message && message.status;
      if (action === "start" && status === "success") {
        setStatus("upstream-connected");
        // Track may already be bound by now; try capturing.
        captureTrack();
      } else if (status === "error") {
        setStatus("upstream-error: " + (message.info || "unknown"));
      }
    },
    onStop: (message) => {
      console.log("[gateway] upstream onStop:", message);
      setStatus("upstream-stopped");
      if (state.onUpstreamTerminated) state.onUpstreamTerminated();
    },
    onTerminate: (message) => {
      console.log("[gateway] upstream onTerminate:", message);
      setStatus("upstream-terminated");
      if (state.onUpstreamTerminated) state.onUpstreamTerminated();
    },
    onUpdate: (message) => {
      console.debug("[gateway] upstream onUpdate:", message);
    },
    onCustomEvent: (message) => {
      console.debug("[gateway] upstream custom event:", message);
    },
  };

  const streamProps = {
    streamSource: lib.StreamType ? lib.StreamType.DIRECT : "direct",
    logLevel:     lib.LogLevel ? lib.LogLevel.INFO : undefined,
    streamConfig,
  };

  try {
    const result = await lib.AppStreamer.connect(streamProps);
    console.log("[gateway] AppStreamer.connect result:", result);
    // connect() resolves once setup is complete; the track normally
    // arrives via loadedmetadata shortly after. captureTrack is also
    // triggered by onStart above.
    return lib.AppStreamer;
  } catch (err) {
    console.error("[gateway] upstream error:", err);
    setStatus("upstream-error: " + (err && err.message ? err.message : String(err)));
    if (state.onUpstreamTerminated) state.onUpstreamTerminated();
    throw err;
  }
}

// ── DOWNSTREAM ────────────────────────────────────────────────
// Standard RTCPeerConnection with iceTransportPolicy=relay. Forwards
// the captured upstream video track to the browser via coturn.
async function startDownstream(videoTrack) {
  if (!videoTrack) {
    throw new Error("startDownstream called without a video track");
  }

  const iceServers = cfg.turnUri
    ? [{
        urls: [
          cfg.turnUri + "?transport=udp",
          cfg.turnUri + "?transport=tcp",
        ],
        username:   cfg.turnUsername,
        credential: cfg.turnCred,
      }]
    : [];

  const pc = new RTCPeerConnection({
    __bypassGatewayPatch: true,
    iceServers,
    iceTransportPolicy: "relay",
  });

  pc.addTrack(videoTrack);

  pc.onicecandidate = (ev) => {
    if (typeof window.gwPageSendIceCandidate === "function") {
      try {
        window.gwPageSendIceCandidate(ev.candidate);
      } catch (err) {
        console.error("[gateway] gwPageSendIceCandidate threw:", err);
      }
    }
  };

  pc.oniceconnectionstatechange = () => {
    console.log("[gateway] downstream iceConnectionState:", pc.iceConnectionState);
  };

  pc.onconnectionstatechange = () => {
    console.log("[gateway] downstream connectionState:", pc.connectionState);
    if (pc.connectionState === "connected") {
      setStatus("downstream-connected");
    } else if (pc.connectionState === "failed" || pc.connectionState === "disconnected") {
      setStatus("downstream-" + pc.connectionState);
    }
  };

  // Exposed to Node (via window globals). Node will call these through
  // page.evaluate after receiving SDP/ICE from the browser client.
  window.gwHandleBrowserOffer = async (sdp) => {
    const offer = typeof sdp === "string"
      ? { type: "offer", sdp }
      : sdp;
    await pc.setRemoteDescription(offer);
    const answer = await pc.createAnswer();
    await pc.setLocalDescription(answer);
    return pc.localDescription && pc.localDescription.sdp;
  };

  window.gwHandleBrowserIceCandidate = async (cand) => {
    if (!cand) return;
    try {
      await pc.addIceCandidate(cand);
    } catch (err) {
      console.error("[gateway] addIceCandidate failed:", err);
    }
  };

  state.downstreamPeer = pc;
  setStatus("downstream-ready");
  return pc;
}

// If both sides are ready — upstream track available and no downstream
// peer yet — create the downstream peer and attach the track.
function maybeStartDownstream() {
  if (!state.upstreamTrack) return;
  if (state.downstreamPeer) return;
  startDownstream(state.upstreamTrack).catch((err) => {
    console.error("[gateway] startDownstream error:", err);
    setStatus("downstream-error: " + (err && err.message ? err.message : String(err)));
  });
}

// ── Browser <-> Node bridge (HC4 wires these to Node) ─────────
// These run inside the page. Node calls them via page.evaluate after
// receiving SDP/ICE from the browser client over its own channel.
window.gwBrowserOffer = async (sdp) => {
  if (!state.downstreamPeer) maybeStartDownstream();
  if (!state.downstreamPeer) {
    throw new Error("downstream peer unavailable (upstream track not yet received)");
  }
  return window.gwHandleBrowserOffer(sdp);
};

window.gwBrowserIceCandidate = async (cand) => {
  if (typeof window.gwHandleBrowserIceCandidate === "function") {
    await window.gwHandleBrowserIceCandidate(cand);
  } else {
    console.warn("[gateway] ICE candidate dropped — downstream peer not ready");
  }
};

// ── Exposed to Node (HC4) ────────────────────────────────────
// Node overwrites these via page.exposeFunction during gateway boot.
window.gwPageSendAnswer = null;
window.gwPageSendIceCandidate = null;

// Boot on load. Because this script is loaded as a module (deferred),
// DOMContentLoaded may have already fired by the time we register the
// handler, so also fall through to boot immediately in that case.
//
// runUpstreamLoop wraps startUpstream() in a retry harness. Kit may not
// be reachable for the first few minutes after pod startup (the user
// starts Kit manually in vscode), and Kit can also die mid-session. We
// reconnect with exponential backoff so the Gateway recovers as soon as
// Kit becomes reachable again.
async function runUpstreamLoop() {
  let backoffMs = 1000;
  const maxBackoffMs = 15000;
  while (true) {
    try {
      setStatus("upstream-connecting");
      state.upstreamPeer = await startUpstream();
      // startUpstream resolves once connect() returns. Now block until the
      // library reports termination via one of the event callbacks.
      await new Promise((resolve) => {
        state.onUpstreamTerminated = resolve;
      });
      state.onUpstreamTerminated = null;
    } catch (err) {
      console.warn("[gateway] upstream error:", err && err.message ? err.message : err);
      state.onUpstreamTerminated = null;
    }
    // Reset track so the downstream peer can be re-plumbed on the next run.
    state.upstreamTrack = null;
    if (state.downstreamPeer) {
      try { state.downstreamPeer.close(); } catch {}
      state.downstreamPeer = null;
    }
    setStatus(`upstream-retry-in-${backoffMs}ms`);
    await new Promise((r) => setTimeout(r, backoffMs));
    backoffMs = Math.min(maxBackoffMs, Math.floor(backoffMs * 1.7));
  }
}

if (document.readyState === "loading") {
  window.addEventListener("DOMContentLoaded", runUpstreamLoop);
} else {
  runUpstreamLoop();
}
