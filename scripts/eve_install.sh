#!/usr/bin/env bash
set -euo pipefail

printf -- "== eve-install ==\n"

if [[ -f /workspace/proto/agent.proto ]]; then
  printf -- '%s\n' "- generating protobufs"
  /workspace/scripts/gen_proto.sh
else
  printf -- '%s\n' "- skipping proto: /workspace/proto/agent.proto not found"
fi

printf -- '%s\n' "- checking models directory"
if [[ -d /workspace/models ]]; then
  ls -1 /workspace/models || true
else
  printf -- '%s\n' "  (models dir missing)"
fi

printf -- "== done ==\n"
