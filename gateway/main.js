// gateway/main.js
// Node orchestrator for the isaac-launchable WebRTC gateway.
//
// Responsibilities:
//   1. Read config from env (KIT_SIGNAL_URL, TURN_URI/USERNAME/CREDENTIAL,
//      LISTEN_ADDR).
//   2. Serve gateway/page/ over HTTP on LISTEN_ADDR, mount signaling WS at
//      /signaling, expose /healthz for liveness.
//   3. Launch headless Chromium via Puppeteer and navigate it to the
//      locally-served index.html with config passed via query string.
//   4. Expose gwPageSendIceCandidate so the page can push downstream ICE
//      candidates back to the currently-connected browser client.
//   5. Relay browser WS signaling (offer / candidate) into the page and
//      pipe answers back out.

import http from "http";
import path from "path";
import fs from "fs/promises";
import { WebSocketServer } from "ws";
import puppeteer from "puppeteer";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const MIME_TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js":   "application/javascript; charset=utf-8",
  ".cjs":  "application/javascript; charset=utf-8",
  ".mjs":  "application/javascript; charset=utf-8",
  ".css":  "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg":  "image/svg+xml",
  ".png":  "image/png",
  ".jpg":  "image/jpeg",
  ".jpeg": "image/jpeg",
  ".ico":  "image/x-icon",
  ".map":  "application/json; charset=utf-8",
};

function mustEnv(name) {
  const v = process.env[name];
  if (!v || v.trim() === "") {
    throw new Error(`env ${name} is required`);
  }
  return v;
}

// "127.0.0.1:9000" or ":9000" → { host, port }. Default host is 0.0.0.0.
function parseListenAddr(v) {
  const trimmed = String(v).trim();
  const idx = trimmed.lastIndexOf(":");
  if (idx === -1) {
    throw new Error(`invalid LISTEN_ADDR: ${v} (expected ":PORT" or "HOST:PORT")`);
  }
  const hostPart = trimmed.slice(0, idx);
  const portPart = trimmed.slice(idx + 1);
  const port = parseInt(portPart, 10);
  if (!Number.isInteger(port) || port <= 0 || port > 65535) {
    throw new Error(`invalid LISTEN_ADDR port: ${portPart}`);
  }
  return {
    host: hostPart === "" ? "0.0.0.0" : hostPart,
    port,
  };
}

function loadConfig() {
  return {
    kitSignalUrl:   mustEnv("KIT_SIGNAL_URL"),
    turnUri:        mustEnv("TURN_URI"),
    turnUsername:   mustEnv("TURN_USERNAME"),
    turnCredential: mustEnv("TURN_CREDENTIAL"),
    listen:         parseListenAddr(process.env.LISTEN_ADDR || ":9000"),
  };
}

function buildStaticHandler(pageDir) {
  return async (req, res) => {
    const urlPath = (req.url || "/").split("?")[0];

    if (urlPath === "/healthz") {
      res.writeHead(200, { "Content-Type": "text/plain; charset=utf-8" });
      res.end("ok");
      return;
    }

    // Resolve the requested file under pageDir and guard against path
    // traversal. path.join normalizes ".." so we compare the resolved
    // path against the base directory before touching the FS.
    const relPath = urlPath === "/" ? "/index.html" : urlPath;
    const resolved = path.resolve(path.join(pageDir, relPath));
    const base = path.resolve(pageDir);
    if (resolved !== base && !resolved.startsWith(base + path.sep)) {
      res.writeHead(403, { "Content-Type": "text/plain; charset=utf-8" });
      res.end("forbidden");
      return;
    }

    try {
      const data = await fs.readFile(resolved);
      const ext = path.extname(resolved).toLowerCase();
      const mime = MIME_TYPES[ext] || "application/octet-stream";
      res.writeHead(200, { "Content-Type": mime });
      res.end(data);
    } catch (err) {
      if (err && err.code === "ENOENT") {
        res.writeHead(404, { "Content-Type": "text/plain; charset=utf-8" });
        res.end("not found");
      } else {
        console.error("[gateway] static serve error:", err);
        res.writeHead(500, { "Content-Type": "text/plain; charset=utf-8" });
        res.end("internal error");
      }
    }
  };
}

function buildPageUrl(cfg) {
  const qs = new URLSearchParams({
    kit_signal_url: cfg.kitSignalUrl,
    turn_uri:       cfg.turnUri,
    turn_username:  cfg.turnUsername,
    turn_cred:      cfg.turnCredential,
  });
  return `http://127.0.0.1:${cfg.listen.port}/index.html?${qs.toString()}`;
}

async function main() {
  const cfg = loadConfig();
  const pageDir = path.join(__dirname, "page");

  // ── HTTP + WS server ────────────────────────────────────────
  const httpServer = http.createServer(buildStaticHandler(pageDir));

  const wss = new WebSocketServer({ noServer: true });
  httpServer.on("upgrade", (req, socket, head) => {
    const urlPath = (req.url || "").split("?")[0];
    if (urlPath === "/signaling") {
      wss.handleUpgrade(req, socket, head, (ws) => {
        wss.emit("connection", ws, req);
      });
    } else {
      socket.destroy();
    }
  });

  await new Promise((resolve, reject) => {
    httpServer.once("error", reject);
    httpServer.listen(cfg.listen.port, cfg.listen.host, () => {
      httpServer.off("error", reject);
      console.log(`[gateway] http+ws listening on ${cfg.listen.host}:${cfg.listen.port}`);
      resolve();
    });
  });

  // ── Puppeteer ───────────────────────────────────────────────
  const browser = await puppeteer.launch({
    headless: "shell",
    args: [
      "--no-sandbox",
      "--disable-dev-shm-usage",
      "--autoplay-policy=no-user-gesture-required",
    ],
  });

  // Use the default page created on launch rather than browser.newPage().
  // With headless: "shell" in Puppeteer 23, newPage() can resolve before
  // the main frame is attached, which makes page.exposeFunction() throw
  // "Requesting main frame too early!". The default page is guaranteed
  // to have a frame.
  const pages = await browser.pages();
  const page = pages[0] ?? await browser.newPage();
  page.on("console", (msg) => {
    console.log(`[page ${msg.type()}] ${msg.text()}`);
  });
  page.on("pageerror", (err) => {
    console.error("[page error]", err);
  });
  page.on("requestfailed", (req) => {
    console.warn(`[page requestfailed] ${req.url()} — ${req.failure()?.errorText}`);
  });

  // Holder for the currently-connected browser WS so the page can push
  // ICE candidates back out. Only one browser is supported at a time.
  let currentBrowserWs = null;

  await page.exposeFunction("gwPageSendIceCandidate", (cand) => {
    // Null candidate = end-of-candidates; still forward so the browser
    // can finalize trickle ICE if it cares.
    if (currentBrowserWs && currentBrowserWs.readyState === 1 /* OPEN */) {
      try {
        currentBrowserWs.send(JSON.stringify({ type: "candidate", candidate: cand }));
      } catch (err) {
        console.error("[gateway] failed to send ICE to browser:", err);
      }
    }
  });

  const pageUrl = buildPageUrl(cfg);
  try {
    await page.goto(pageUrl, { waitUntil: "domcontentloaded", timeout: 30_000 });
    console.log(`[gateway] page loaded: ${pageUrl}`);
  } catch (err) {
    console.error("[gateway] failed to load page:", err);
    throw err;
  }

  // ── Browser signaling relay ────────────────────────────────
  wss.on("connection", (ws, req) => {
    const remote = req.socket.remoteAddress;
    console.log(`[gateway] browser connected from ${remote}`);

    if (currentBrowserWs && currentBrowserWs.readyState === 1) {
      console.warn("[gateway] replacing previous browser connection");
      try { currentBrowserWs.close(1000, "replaced"); } catch {}
    }
    currentBrowserWs = ws;

    ws.on("message", async (raw) => {
      let msg;
      try {
        msg = JSON.parse(raw.toString());
      } catch (err) {
        console.warn("[gateway] ignoring non-JSON WS message:", err.message);
        return;
      }

      try {
        if (msg.type === "offer") {
          const answerSdp = await page.evaluate(
            (sdp) => window.gwBrowserOffer(sdp),
            msg.sdp,
          );
          if (ws.readyState === 1) {
            ws.send(JSON.stringify({ type: "answer", sdp: answerSdp }));
          }
        } else if (msg.type === "candidate") {
          await page.evaluate(
            (c) => window.gwBrowserIceCandidate(c),
            msg.candidate,
          );
        } else {
          console.warn(`[gateway] unknown signaling message type: ${msg.type}`);
        }
      } catch (err) {
        console.error("[gateway] signaling error:", err);
      }
    });

    ws.on("close", (code, reason) => {
      console.log(`[gateway] browser disconnected code=${code} reason=${reason?.toString() || ""}`);
      if (currentBrowserWs === ws) {
        currentBrowserWs = null;
      }
    });

    ws.on("error", (err) => {
      console.error("[gateway] browser WS error:", err);
    });
  });

  // ── Graceful shutdown ──────────────────────────────────────
  let shuttingDown = false;
  const shutdown = async (signal) => {
    if (shuttingDown) return;
    shuttingDown = true;
    console.log(`[gateway] received ${signal}, shutting down`);

    try { wss.close(); } catch (err) { console.error("[gateway] wss.close error:", err); }
    try { await browser.close(); } catch (err) { console.error("[gateway] browser.close error:", err); }
    try { httpServer.close(); } catch (err) { console.error("[gateway] httpServer.close error:", err); }

    // Give close callbacks a short tick to flush, then exit.
    setTimeout(() => process.exit(0), 200).unref();
  };
  process.on("SIGINT",  () => shutdown("SIGINT"));
  process.on("SIGTERM", () => shutdown("SIGTERM"));
}

main().catch((err) => {
  console.error("[gateway] fatal:", err);
  process.exit(1);
});
