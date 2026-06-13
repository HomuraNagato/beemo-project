.PHONY: proto init init-cpu init-gpu start start-gpu start-cpu stop logs

proto:
	./scripts/gen_proto.sh

init:
	./scripts/beemo-init.sh cpu

init-cpu:
	./scripts/beemo-init.sh cpu

init-gpu:
	./scripts/beemo-init.sh gpu

start:
	./scripts/beemo-start.sh gpu

start-gpu:
	./scripts/beemo-start.sh gpu

start-cpu:
	./scripts/beemo-start.sh cpu

stop:
	docker compose -f docker-compose.yaml -f docker-compose.gpu.yaml stop eve-wakeword eve-asr eve-orchestrator eve-vllm eve-embedding

logs:
	docker logs -f eve-orchestrator
