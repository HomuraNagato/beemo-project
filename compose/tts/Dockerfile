FROM docker.io/library/node:24-bookworm-slim AS build

WORKDIR /src
COPY services/tts/package.json services/tts/package-lock.json ./
RUN npm ci
COPY services/tts/tsconfig.json ./
COPY services/tts/src ./src
RUN npm run check && npm run build

FROM docker.io/library/node:24-bookworm-slim

ENV NODE_ENV=production \
    BEEMO_PROTO_PATH=/app/proto/agent.proto \
    KOKORO_MODEL_DIR=/models/kokoro-int8-en-v0_19

WORKDIR /app
COPY services/tts/package.json services/tts/package-lock.json ./
RUN npm ci --omit=dev
COPY --from=build /src/dist ./dist
COPY proto/agent.proto /app/proto/agent.proto

EXPOSE 5018
CMD ["node", "dist/server.js"]
