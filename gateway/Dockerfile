# syntax=docker/dockerfile:1.6
FROM node:22-alpine

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci --omit=dev

COPY main.js ./

RUN addgroup -S gateway && adduser -S -G gateway gateway
USER gateway

ENV NODE_ENV=production \
    LISTEN_ADDR=":9000" \
    KIT_SIGNAL_URL="ws://127.0.0.1:49100"

EXPOSE 9000

CMD ["node", "main.js"]
