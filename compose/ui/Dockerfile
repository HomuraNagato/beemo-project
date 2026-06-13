FROM python:3.11-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1 \
    PYTHONPATH=/app/generated:/app

WORKDIR /app

COPY compose/ui/requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir -r /tmp/requirements.txt

RUN mkdir -p /app/eve_proto /app/generated
COPY proto/agent.proto /app/eve_proto/agent.proto
RUN python -m grpc_tools.protoc \
    -I/app \
    --python_out=/app/generated \
    --grpc_python_out=/app/generated \
    /app/eve_proto/agent.proto

COPY services/ui /app/services/ui
COPY web/beemo /app/web/beemo

EXPOSE 5017

CMD ["python", "-m", "services.ui.server"]
