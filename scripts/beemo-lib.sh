#!/usr/bin/env bash

beemo_default_profile() {
  if [ -n "${BEEMO_PROFILE:-}" ]; then
    printf '%s\n' "$BEEMO_PROFILE"
    return
  fi
  printf 'vllm-gpu\n'
}

beemo_set_profile() {
  local requested="${1:-$(beemo_default_profile)}"
  case "$requested" in
    vllm-gpu)
      BEEMO_PROFILE_NAME="vllm-gpu"
      BEEMO_PROFILE_RUNTIME="vllm"
      BEEMO_PROFILE_ACCEL="gpu"
      BEEMO_COMPOSE_FILES=(-f docker-compose.yaml -f docker-compose.gpu.yaml)
      ;;
    vllm-cpu)
      BEEMO_PROFILE_NAME="vllm-cpu"
      BEEMO_PROFILE_RUNTIME="vllm"
      BEEMO_PROFILE_ACCEL="cpu"
      BEEMO_COMPOSE_FILES=(-f docker-compose.yaml -f docker-compose.cpu.yaml -f docker-compose.cpu.vllm.yaml)
      ;;
    llama-cpu)
      BEEMO_PROFILE_NAME="llama-cpu"
      BEEMO_PROFILE_RUNTIME="llama"
      BEEMO_PROFILE_ACCEL="cpu"
      BEEMO_COMPOSE_FILES=(-f docker-compose.yaml -f docker-compose.cpu.yaml -f docker-compose.cpu.llamacpp.yaml)
      ;;
    *)
      printf 'invalid profile %q; expected vllm-gpu, vllm-cpu, or llama-cpu\n' "$requested" >&2
      return 2
      ;;
  esac
}
