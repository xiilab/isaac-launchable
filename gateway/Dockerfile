# syntax=docker/dockerfile:1.6
FROM node:22-bookworm AS build

WORKDIR /app

# NVIDIA private registry for @nvidia/omniverse-webrtc-streaming-library,
# public registry for everything else. Matches isaac-lab/web-viewer-sample.
RUN echo 'registry=https://registry.npmjs.org/' > /root/.npmrc && \
    echo '@nvidia:registry=https://edge.urm.nvidia.com/artifactory/api/npm/omniverse-client-npm-local/' >> /root/.npmrc

# Install gateway runtime deps.
COPY package.json package-lock.json ./
RUN npm ci --omit=dev

# Pull the NVIDIA streaming library at a pinned version and extract the UMD
# bundle into the page's vendor directory so the headless page can <script>
# src it at runtime.
ARG OV_STREAMING_LIB_VERSION=5.16.3
RUN mkdir -p /tmp/ov && \
    npm pack @nvidia/omniverse-webrtc-streaming-library@${OV_STREAMING_LIB_VERSION} --pack-destination /tmp/ov && \
    tar -xzf /tmp/ov/nvidia-omniverse-webrtc-streaming-library-${OV_STREAMING_LIB_VERSION}.tgz -C /tmp/ov && \
    mkdir -p page/vendor && \
    cp /tmp/ov/package/dist/omniverse-webrtc-streaming-library.umd.cjs page/vendor/ && \
    rm -rf /tmp/ov

# Copy gateway source last so code changes don't bust the npm-install cache.
COPY main.js ./
COPY page ./page

# Second stage: same node:22-bookworm runtime. Puppeteer's bundled Chromium
# lives at /root/.cache/puppeteer — preserved via copy below. Using a single
# stage keeps the image size the same while giving us a clear split.
FROM node:22-bookworm

# System deps Chromium needs at runtime. Sourced from
# https://pptr.dev/troubleshooting#chrome-doesnt-launch-on-linux.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates fonts-liberation libasound2 libatk-bridge2.0-0 \
      libatk1.0-0 libatspi2.0-0 libcairo2 libcups2 libdbus-1-3 \
      libdrm2 libgbm1 libglib2.0-0 libgtk-3-0 libnspr4 libnss3 \
      libpango-1.0-0 libx11-6 libxcb1 libxcomposite1 libxdamage1 \
      libxext6 libxfixes3 libxkbcommon0 libxrandr2 wget xdg-utils \
    && rm -rf /var/lib/apt/lists/*

# Non-root user (puppeteer's bundled chromium will run under this uid).
RUN useradd --create-home --shell /bin/bash gateway

WORKDIR /app
COPY --from=build --chown=gateway:gateway /app /app

# Bring puppeteer's chromium cache from the build stage.
COPY --from=build --chown=gateway:gateway /root/.cache/puppeteer /home/gateway/.cache/puppeteer

USER gateway

# Defaults; real values come from the pod env.
ENV NODE_ENV=production \
    LISTEN_ADDR=":9000" \
    PUPPETEER_CACHE_DIR=/home/gateway/.cache/puppeteer

EXPOSE 9000

CMD ["node", "main.js"]
