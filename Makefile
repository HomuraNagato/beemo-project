.PHONY: proto start start-gpu start-cpu start-full start-full-cpu stop logs

proto:
	./scripts/gen_proto.sh

start:
	./scripts/beemo-start.sh gpu

start-gpu:
	./scripts/beemo-start.sh gpu

start-cpu:
	./scripts/beemo-start.sh cpu

start-full:
	./scripts/beemo-start.sh gpu --db

start-full-cpu:
	./scripts/beemo-start.sh cpu --db

stop:
	docker compose -f docker-compose.yaml -f docker-compose.gpu.yaml -f docker-compose.pensieve.yaml stop eve-wakeword eve-asr eve-orchestrator eve-vllm eve-embedding pensieve

logs:
	docker logs -f eve-orchestrator
