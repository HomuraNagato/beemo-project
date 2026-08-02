.PHONY: build install proto init init-cpu init-gpu start start-vllm-gpu start-vllm-cpu start-llama-cpu status doctor stop logs

build:
	mkdir -p bin
	go build -o bin/beemo ./cmd/beemo
	go build -o bin/beemo-code ./cmd/beemo-code
	go build -o bin/beemo-chat ./src/tui

install: build
	install -Dm755 bin/beemo "$(HOME)/.local/bin/beemo"
	install -Dm755 bin/beemo-code "$(HOME)/.local/bin/beemo-code"
	install -Dm755 bin/beemo-chat "$(HOME)/.local/bin/beemo-chat"

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
