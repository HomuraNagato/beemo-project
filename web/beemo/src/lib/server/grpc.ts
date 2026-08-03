import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import { resolve } from "node:path";

type Message = Record<string, unknown>;
type UnaryMethod = (request: Message, callback: (error: grpc.ServiceError | null, response: Message) => void) => void;
type StreamMethod = (request: Message) => grpc.ClientReadableStream<Message>;
type DynamicClient = grpc.Client & Record<string, unknown>;

const protoPath = process.env.BEEMO_PROTO_PATH || resolve(process.cwd(), "../../proto/agent.proto");
const definition = protoLoader.loadSync(protoPath, {
  defaults: true,
  enums: String,
  keepCase: false,
  longs: Number,
  oneofs: true,
});
const packages = grpc.loadPackageDefinition(definition) as unknown as Record<string, Record<string, unknown>>;
const eve = packages.eve;

function serviceConstructor(name: string): grpc.ServiceClientConstructor {
  const constructor = eve?.[name];
  if (typeof constructor !== "function") {
    throw new Error(`gRPC service ${name} is unavailable`);
  }
  return constructor as grpc.ServiceClientConstructor;
}

export function orchestratorClient(): DynamicClient {
  const Constructor = serviceConstructor("Orchestrator");
  return new Constructor(
    process.env.ORCH_ADDR || "eve-orchestrator:5013",
    grpc.credentials.createInsecure(),
  ) as DynamicClient;
}

export function wakeWordClient(): DynamicClient {
  const Constructor = serviceConstructor("WakeWord");
  return new Constructor(
    process.env.WAKEWORD_ADDR || "eve-wakeword:5020",
    grpc.credentials.createInsecure(),
  ) as DynamicClient;
}

export function unary(client: DynamicClient, methodName: string, request: Message): Promise<Message> {
  const method = client[methodName];
  if (typeof method !== "function") {
    return Promise.reject(new Error(`gRPC method ${methodName} is unavailable`));
  }
  return new Promise((resolveResponse, rejectResponse) => {
    (method as UnaryMethod).call(client, request, (error, response) => {
      if (error) rejectResponse(error);
      else resolveResponse(response);
    });
  });
}

export function serverStream(client: DynamicClient, methodName: string, request: Message): grpc.ClientReadableStream<Message> {
  const method = client[methodName];
  if (typeof method !== "function") {
    throw new Error(`gRPC method ${methodName} is unavailable`);
  }
  return (method as StreamMethod).call(client, request);
}

export function record(value: unknown): Message {
  return value && typeof value === "object" ? value as Message : {};
}

export function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

export function number(value: unknown): number {
  return typeof value === "number" ? value : Number(value) || 0;
}

export function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.map(text).filter(Boolean) : [];
}
