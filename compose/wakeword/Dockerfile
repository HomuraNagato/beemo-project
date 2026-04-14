FROM python:3.11-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1 \
    PYTHONPATH=/app/generated:/app

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    pulseaudio-utils \
    && rm -rf /var/lib/apt/lists/*

COPY compose/wakeword/requirements.txt /tmp/requirements.txt
RUN pip install --no-cache-dir -r /tmp/requirements.txt
RUN python -c "import os, openwakeword; from openwakeword.utils import download_file; target=os.path.join(os.path.dirname(openwakeword.__file__), 'resources', 'models'); os.makedirs(target, exist_ok=True); urls=[]; [urls.extend([m['download_url'], m['download_url'].replace('.tflite', '.onnx')]) for m in openwakeword.FEATURE_MODELS.values()]; [download_file(url, target) for url in urls if not os.path.exists(os.path.join(target, url.split('/')[-1]))]"

RUN mkdir -p /app/eve_proto /app/generated
COPY proto/agent.proto /app/eve_proto/agent.proto
RUN python -m grpc_tools.protoc \
    -I/app \
    --python_out=/app/generated \
    --grpc_python_out=/app/generated \
    /app/eve_proto/agent.proto

COPY services /app/services

EXPOSE 5020

CMD ["python", "-m", "services.wakeword.service"]
