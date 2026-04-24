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
//      Node-exposed functions (window.gwNodeSend, etc.) added by
//      Puppeteer's page.exposeFunction in gateway/main.js.

const cfg = readConfigFromQueryString();

const state = {
  status: "initializing",
  upstreamPeer: null,
  downstreamPeer: null,
  upstreamTrack: null,
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

// ── UPSTREAM (filled in HC3) ─────────────────────────────────
async function startUpstream() {
  // To implement in HC3 using window.OVWebStreamingLibrary (UMD global).
  setStatus("upstream-stub");
  return null;
}

// ── DOWNSTREAM (filled in HC3/HC4) ───────────────────────────
async function startDownstream(_upstreamTrack) {
  // To implement in HC3/HC4 with standard RTCPeerConnection +
  // iceTransportPolicy: 'relay' + iceServers from cfg.
  setStatus("downstream-stub");
  return null;
}

// ── Browser <-> Node bridge (filled in HC4) ──────────────────
// These globals are expected to be provided by Puppeteer's
// page.exposeFunction at runtime; no-op fallback for standalone test.
window.gwBrowserOffer ??= async (_sdp) => {
  console.warn("[gateway] gwBrowserOffer not bridged yet");
};
window.gwBrowserIceCandidate ??= async (_cand) => {
  console.warn("[gateway] gwBrowserIceCandidate not bridged yet");
};

// ── Exposed to Node (HC4) ────────────────────────────────────
window.gwPageSendAnswer = null;       // Node will overwrite via exposeFunction
window.gwPageSendIceCandidate = null; // Node will overwrite via exposeFunction

// Boot on load.
window.addEventListener("DOMContentLoaded", async () => {
  setStatus("booting");
  try {
    state.upstreamPeer = await startUpstream();
    // Downstream started lazily when browser connects (handled in HC4).
    setStatus("ready");
  } catch (err) {
    console.error("[gateway] boot error:", err);
    setStatus("boot-error: " + err.message);
  }
});
