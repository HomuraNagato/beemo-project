import logging
import os
import signal
from concurrent import futures

import grpc
import numpy as np
from eve_proto import agent_pb2, agent_pb2_grpc
from faster_whisper import WhisperModel


def env_bool(name: str, default: bool) -> bool:
    raw = os.getenv(name)
    if raw is None:
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


class ASRServicer(agent_pb2_grpc.ASRServicer):
    def __init__(
        self,
        model: WhisperModel,
        default_sample_rate_hz: int,
        language: str,
        beam_size: int,
        vad_filter: bool,
    ) -> None:
        self.model = model
        self.default_sample_rate_hz = default_sample_rate_hz
        self.language = language.strip() or None
        self.beam_size = beam_size
        self.vad_filter = vad_filter

    def StreamTranscribe(self, request_iterator, context):
        sample_rate_hz = 0
        chunks = []
        total_bytes = 0
        for chunk in request_iterator:
            if chunk.sample_rate_hz:
                if sample_rate_hz and chunk.sample_rate_hz != sample_rate_hz:
                    context.abort(grpc.StatusCode.INVALID_ARGUMENT, "mixed sample_rate_hz not supported")
                sample_rate_hz = chunk.sample_rate_hz
            if chunk.pcm_s16le:
                chunks.append(chunk.pcm_s16le)
                total_bytes += len(chunk.pcm_s16le)

        if not chunks:
            yield agent_pb2.TranscribeResult(text="", confidence=0.0, is_final=True)
            return

        if sample_rate_hz <= 0:
            sample_rate_hz = self.default_sample_rate_hz

        audio = np.frombuffer(b"".join(chunks), dtype=np.int16).astype(np.float32) / 32768.0
        if audio.size == 0:
            yield agent_pb2.TranscribeResult(text="", confidence=0.0, is_final=True)
            return

        segments, info = self.model.transcribe(
            audio,
            language=self.language,
            beam_size=self.beam_size,
            vad_filter=self.vad_filter,
            condition_on_previous_text=False,
        )
        text = " ".join(segment.text.strip() for segment in segments if segment.text.strip()).strip()
        confidence = float(getattr(info, "language_probability", 0.0) or 0.0)
        logging.info(
            "asr.transcribed bytes=%d sample_rate_hz=%d text=%r confidence=%.2f",
            total_bytes,
            sample_rate_hz,
            text,
            confidence,
        )
        yield agent_pb2.TranscribeResult(text=text, confidence=confidence, is_final=True)


def build_model() -> WhisperModel:
    model_name = os.getenv("ASR_MODEL", "tiny.en")
    device = os.getenv("ASR_DEVICE", "cpu")
    compute_type = os.getenv("ASR_COMPUTE_TYPE", "int8")
    download_root = os.getenv("ASR_DOWNLOAD_ROOT", "/models/faster-whisper")
    logging.info(
        "asr.model loading model=%s device=%s compute_type=%s download_root=%s",
        model_name,
        device,
        compute_type,
        download_root,
    )
    return WhisperModel(
        model_name,
        device=device,
        compute_type=compute_type,
        download_root=download_root,
    )


def main() -> int:
    logging.basicConfig(
        level=os.getenv("LOG_LEVEL", "INFO").upper(),
        format="%(asctime)s %(levelname)s %(message)s",
    )

    model = build_model()
    servicer = ASRServicer(
        model=model,
        default_sample_rate_hz=int(os.getenv("WAKEWORD_SAMPLE_RATE_HZ", "16000")),
        language=os.getenv("ASR_LANGUAGE", "en"),
        beam_size=int(os.getenv("ASR_BEAM_SIZE", "1")),
        vad_filter=env_bool("ASR_VAD_FILTER", False),
    )

    host = os.getenv("ASR_HOST", "0.0.0.0")
    port = int(os.getenv("ASR_PORT", "5019"))
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    agent_pb2_grpc.add_ASRServicer_to_server(servicer, server)
    server.add_insecure_port(f"{host}:{port}")
    server.start()
    logging.info("asr.server listening=%s:%d", host, port)

    def stop_handler(signum, _frame):
        logging.info("asr.server stopping signal=%s", signum)
        server.stop(grace=2)

    signal.signal(signal.SIGINT, stop_handler)
    signal.signal(signal.SIGTERM, stop_handler)
    server.wait_for_termination()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
