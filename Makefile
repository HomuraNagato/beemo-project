.PHONY: proto init init-cpu init-gpu start start-vllm-gpu start-vllm-cpu start-llama-cpu status doctor stop logs

proto:
	./scripts/gen_proto.sh

init:
	./scripts/beemo-init.sh cpu

init-cpu:
	./scripts/beemo-init.sh cpu

init-gpu:
	./scripts/beemo-init.sh gpu

start:
	./scripts/beemo-start.sh vllm-gpu

start-vllm-gpu:
	./scripts/beemo-start.sh vllm-gpu

start-vllm-cpu:
	./scripts/beemo-start.sh vllm-cpu

start-llama-cpu:
	./scripts/beemo-start.sh llama-cpu

status:
	./scripts/beemo-status.sh

doctor:
	./scripts/beemo-doctor.sh

stop:
	./scripts/beemo-stop.sh

logs:
	./scripts/beemo-logs.sh eve-orchestrator
