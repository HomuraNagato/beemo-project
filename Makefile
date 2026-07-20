.PHONY: build install proto init init-cpu init-gpu start start-bge start-vllm-gpu start-vllm-cpu start-llama-cpu status doctor stop logs

build:
	mkdir -p bin
	go build -o bin/beemo ./cmd/beemo

install: build
	install -Dm755 bin/beemo "$(HOME)/.local/bin/beemo"

proto:
	./scripts/gen_proto.sh

init:
	./scripts/beemo-init.sh cpu

init-cpu:
	./scripts/beemo-init.sh cpu

init-gpu:
	./scripts/beemo-init.sh gpu

start:
	./scripts/beemo.sh up --profile garnetmoon

start-bge:
	./scripts/beemo.sh up --profile garnetmoon-bge

start-vllm-gpu:
	./scripts/beemo.sh up --profile vllm-gpu

start-vllm-cpu:
	./scripts/beemo.sh up --profile vllm-cpu

start-llama-cpu:
	./scripts/beemo.sh up --profile llama-cpu

status:
	./scripts/beemo.sh status

doctor:
	./scripts/beemo.sh doctor

stop:
	./scripts/beemo.sh down

logs:
	./scripts/beemo.sh logs eve-orchestrator
