import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { GenerationConfig, OfflineTts } from "sherpa-onnx-node";

interface SpeakRequest {
  sessionId?: string;
  text?: string;
  options?: Record<string, string>;
}

type SpeakCall = grpc.ServerWritableStream<SpeakRequest, Record<string, unknown>>;

const modelDir = process.env.KOKORO_MODEL_DIR || "/models/kokoro-int8-en-v0_19";
const modelPath = process.env.KOKORO_MODEL_PATH || resolve(modelDir, "model.int8.onnx");
const voicesPath = resolve(modelDir, "voices.bin");
const tokensPath = resolve(modelDir, "tokens.txt");
const dataDir = resolve(modelDir, "espeak-ng-data");
const defaultSpeaker = integer(process.env.KOKORO_SPEAKER_ID, 4);
const defaultSpeed = decimal(process.env.KOKORO_SPEED, 1.05);
const numThreads = integer(process.env.KOKORO_NUM_THREADS, 4);
const maxTextChars = integer(process.env.TTS_MAX_TEXT_CHARS, 4000);

for (const path of [modelPath, voicesPath, tokensPath, dataDir]) {
  if (!existsSync(path)) throw new Error(`Kokoro model asset is missing: ${path}. Run beemo init.`);
}

const engine = new OfflineTts({
  model: {
    kokoro: { model: modelPath, voices: voicesPath, tokens: tokensPath, dataDir },
    debug: false,
    numThreads,
    provider: "cpu",
  },
  maxNumSentences: 1,
});

function integer(raw: string | undefined, fallback: number): number {
  const parsed = Number.parseInt(raw || "", 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function decimal(raw: string | undefined, fallback: number): number {
  const parsed = Number.parseFloat(raw || "");
  return Number.isFinite(parsed) ? parsed : fallback;
}

function pcm16(samples: Float32Array): Buffer {
  const output = Buffer.allocUnsafe(samples.length * 2);
  for (let index = 0; index < samples.length; index += 1) {
    const sample = Math.max(-1, Math.min(1, samples[index] ?? 0));
    output.writeInt16LE(Math.round(sample * (sample < 0 ? 32768 : 32767)), index * 2);
  }
  return output;
}

function speak(call: SpeakCall): void {
  const text = (call.request.text || "").trim();
  if (!text) {
    call.destroy(new Error("text is required"));
    return;
  }
  if (text.length > maxTextChars) {
    call.destroy(new Error(`text exceeds ${maxTextChars} characters`));
    return;
  }

  try {
    const options = call.request.options || {};
    const generationConfig = new GenerationConfig({
      sid: integer(options.speaker_id, defaultSpeaker),
      speed: decimal(options.speed, defaultSpeed),
      silenceScale: decimal(options.silence_scale, 0.2),
    });
    const started = performance.now();
    const audio = engine.generate({ text, generationConfig });
    const encoded = pcm16(audio.samples);
    const chunkBytes = 64 * 1024;
    for (let offset = 0; offset < encoded.length; offset += chunkBytes) {
      call.write({
        pcmS16le: encoded.subarray(offset, offset + chunkBytes),
        sampleRateHz: audio.sampleRate,
      });
    }
    call.end();
    const elapsedMs = Math.round(performance.now() - started);
    console.log(`tts.synthesized chars=${text.length} samples=${audio.samples.length} sample_rate_hz=${audio.sampleRate} elapsed_ms=${elapsedMs}`);
  } catch (error) {
    console.error("tts.synthesis_error", error);
    call.destroy(error instanceof Error ? error : new Error(String(error)));
  }
}

const protoPath = process.env.BEEMO_PROTO_PATH || "/app/proto/agent.proto";
const definition = protoLoader.loadSync(protoPath, {
  defaults: true,
  enums: String,
  keepCase: false,
  longs: Number,
  oneofs: true,
});
const packages = grpc.loadPackageDefinition(definition) as unknown as Record<string, Record<string, grpc.ServiceClientConstructor>>;
const ttsService = packages.eve?.TTS?.service;
if (!ttsService) throw new Error("eve.TTS service is unavailable in agent.proto");

const server = new grpc.Server();
server.addService(ttsService, { speak });
const address = process.env.TTS_ADDRESS || "0.0.0.0:5018";
server.bindAsync(address, grpc.ServerCredentials.createInsecure(), (error, port) => {
  if (error) throw error;
  console.log(`tts.server listening=${address} port=${port} model=kokoro-int8-en-v0_19 speaker_id=${defaultSpeaker}`);
});

let stopping = false;
function stop(signal: string): void {
  if (stopping) return;
  stopping = true;
  console.log(`tts.server stopping signal=${signal}`);
  const deadline = setTimeout(() => {
    server.forceShutdown();
    process.exit(0);
  }, 2000);
  deadline.unref();
  server.tryShutdown(() => {
    clearTimeout(deadline);
    process.exit(0);
  });
}

process.on("SIGINT", () => stop("SIGINT"));
process.on("SIGTERM", () => stop("SIGTERM"));
