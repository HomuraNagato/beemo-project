FROM docker.io/library/node:24-bookworm-slim AS build

ENV BEEMO_PROTO_PATH=/src/proto/agent.proto

WORKDIR /src
COPY web/beemo/package.json web/beemo/package-lock.json ./
RUN npm ci
COPY web/beemo ./
COPY proto/agent.proto /src/proto/agent.proto
RUN npm run check && npm run test && npm run build

FROM docker.io/library/node:24-bookworm-slim

ENV NODE_ENV=production \
    HOST=0.0.0.0 \
    PORT=5017 \
    BEEMO_PROTO_PATH=/app/proto/agent.proto

WORKDIR /app
COPY web/beemo/package.json web/beemo/package-lock.json ./
RUN npm ci --omit=dev
COPY --from=build /src/build ./build
COPY proto/agent.proto /app/proto/agent.proto

EXPOSE 5017

CMD ["node", "build"]
