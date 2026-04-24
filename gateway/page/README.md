# gateway/page — Headless Chromium page

The Node orchestrator (`../main.js`) launches Puppeteer, navigates a headless
Chromium to `index.html`, and exchanges SDP/ICE messages with it through
`page.exposeFunction`.

`vendor/omniverse-webrtc-streaming-library.umd.cjs` is NOT committed; it is
populated at container build time from the NVIDIA npm package (see Dockerfile).

HC3/HC4 tasks fill in the upstream + downstream peer logic in `gateway.js`.
