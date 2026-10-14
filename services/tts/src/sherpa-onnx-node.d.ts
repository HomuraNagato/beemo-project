declare module "sherpa-onnx-node" {
  export interface GeneratedAudio {
    samples: Float32Array;
    sampleRate: number;
  }

  export class GenerationConfig {
    constructor(options: { sid: number; speed: number; silenceScale: number });
  }

  export class OfflineTts {
    constructor(config: Record<string, unknown>);
    generate(options: { text: string; generationConfig: GenerationConfig }): GeneratedAudio;
  }
}
