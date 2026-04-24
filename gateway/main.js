// gateway/main.js
// Minimal WebSocket proxy between the browser (via ingress /pod-0/signaling)
// and Kit's /sign_in endpoint on the pod loopback.
//
// Browser ─┐
//          │ WS  /signaling?<query>         ┌─ WS  ws://127.0.0.1:<KIT_PORT>/sign_in?<query>
//          ▼                               ▼
//    [this process]  frame-by-frame pass-through   [Kit omni.kit.livestream.app]
//
// Config via env:
//   KIT_SIGNAL_URL   base URL for Kit's signaling WS (default "ws://127.0.0.1:49100")
//                    NOTE: no "/sign_in" suffix — proxy preserves the browser's path+query.
//   LISTEN_ADDR      "[host:]port" to bind (default ":9000")

import http from "http";
import { WebSocketServer, WebSocket } from "ws";
import { URL } from "url";

function parseListenAddr(v) {
  const [host, port] = v.split(":");
  return { host: host || "0.0.0.0", port: parseInt(port, 10) || 9000 };
}

// WebSocket close-code validation: some codes (1005/1006) are reserved and
// must not be sent on the wire. Pass them through as a generic 1000 so ws
// library does not throw.
function safeCloseCode(code) {
  if (typeof code !== "number") return 1000;
  if (code < 1000 || code >= 5000) return 1000;
  if (code === 1005 || code === 1006) return 1000;
  return code;
}

const KIT_SIGNAL_URL = process.env.KIT_SIGNAL_URL || "ws://127.0.0.1:49100";
const listen = parseListenAddr(process.env.LISTEN_ADDR || ":9000");

const httpServer = http.createServer((req, res) => {
  if (req.url === "/healthz") {
    res.writeHead(200, { "Content-Type": "text/plain" });
    res.end("ok");
    return;
  }
  res.writeHead(404);
  res.end();
});

const wss = new WebSocketServer({ noServer: true });

httpServer.on("upgrade", (req, socket, head) => {
  const reqUrl = new URL(req.url, `http://${req.headers.host || "localhost"}`);
  // Accept any path — ingress already restricts this container's public
  // surface. The NVIDIA library concatenates its own suffix ("/sign_in")
  // onto `signalingPath`, so we see paths like "/pod-0/signaling/sign_in"
  // here; the entire path (minus the ingress-stripped prefix) is irrelevant
  // — we always forward to Kit's "/sign_in" endpoint with the original
  // query string.
  wss.handleUpgrade(req, socket, head, (clientWs) => {
    handleClient(clientWs, reqUrl);
  });
});

function handleClient(clientWs, reqUrl) {
  // Build the upstream URL: kit_signal_url + "/sign_in" + browser's query string.
  const query = reqUrl.searchParams.toString();
  const upstreamUrl =
    KIT_SIGNAL_URL.replace(/\/+$/, "") + "/sign_in" + (query ? "?" + query : "");
  console.log(`[gateway] client connected, opening upstream ${upstreamUrl}`);

  const upstream = new WebSocket(upstreamUrl);

  let upstreamReady = false;
  const clientBuffer = [];

  upstream.on("open", () => {
    upstreamReady = true;
    for (const m of clientBuffer) upstream.send(m);
    clientBuffer.length = 0;
    console.log("[gateway] upstream open");
  });

  upstream.on("message", (m) => {
    if (clientWs.readyState === WebSocket.OPEN) clientWs.send(m);
  });
  upstream.on("close", (code, reason) => {
    console.log(`[gateway] upstream close ${code} ${reason}`);
    if (clientWs.readyState === WebSocket.OPEN) clientWs.close(safeCloseCode(code));
  });
  upstream.on("error", (err) => {
    console.warn("[gateway] upstream error:", err.message);
  });

  clientWs.on("message", (m) => {
    if (upstreamReady && upstream.readyState === WebSocket.OPEN) {
      upstream.send(m);
    } else {
      clientBuffer.push(m);
    }
  });
  clientWs.on("close", (code) => {
    console.log(`[gateway] client close ${code}`);
    if (upstream.readyState === WebSocket.OPEN) upstream.close(safeCloseCode(code));
  });
  clientWs.on("error", (err) => {
    console.warn("[gateway] client error:", err.message);
  });
}

httpServer.listen(listen.port, listen.host, () => {
  console.log(`[gateway] listening on ${listen.host}:${listen.port}, upstream=${KIT_SIGNAL_URL}/sign_in`);
});

function shutdown() {
  console.log("[gateway] shutting down");
  try { wss.close(); } catch {}
  try { httpServer.close(); } catch {}
  process.exit(0);
}
process.on("SIGINT", shutdown);
process.on("SIGTERM", shutdown);
