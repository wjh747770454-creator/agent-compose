import process from "node:process";
import { type output as ZodOutput, type ZodType } from "zod";
import { isPlainJsonObject, normalizeOutputSchema, type RuntimeJsonSchema, type RuntimeOutputSchema } from "./schema.js";

export type RuntimeDelegateOutputSchema = RuntimeOutputSchema;

export interface RuntimeDelegateOptions<S extends RuntimeDelegateOutputSchema = RuntimeDelegateOutputSchema> {
  outputSchema?: S;
  timeoutMs?: number;
  idempotencyKey?: string;
  reason?: string;
}

export interface RuntimeDelegationError {
  classification: string;
  message: string;
}

export interface RuntimeDelegateResult<T = unknown> {
  childRunId: string;
  parentRunId: string;
  rootRunId: string;
  delegationId: string;
  attempt: number;
  attemptRunIds: string[];
  status: string;
  output: string;
  resultJson: string;
  structuredResult: T | null;
  warnings: string[];
  error: RuntimeDelegationError | null;
}

interface DelegationWireResponse {
  childRunId?: string;
  parentRunId?: string;
  rootRunId?: string;
  delegationId?: string;
  attempt?: number;
  attemptRunIds?: string[];
  status?: string;
  output?: string;
  resultJson?: string;
  structuredResult?: unknown;
  warnings?: string[];
  error?: RuntimeDelegationError;
}

export async function delegate<S extends ZodType>(targetAgent: string, prompt: string, options: RuntimeDelegateOptions<S> & { outputSchema: S }): Promise<RuntimeDelegateResult<ZodOutput<S>>>;
export async function delegate<T = unknown>(targetAgent: string, prompt: string, options?: RuntimeDelegateOptions<RuntimeJsonSchema>): Promise<RuntimeDelegateResult<T>>;
export async function delegate<T = unknown>(targetAgent: string, prompt: string, options: RuntimeDelegateOptions = {}): Promise<RuntimeDelegateResult<T>> {
  targetAgent = targetAgent.trim();
  prompt = prompt.trim();
  if (!targetAgent || !prompt) {
    throw new Error("delegate targetAgent and prompt are required");
  }
  const baseURL = requiredEnv("AGENT_COMPOSE_RUNTIME_BASE_URL").replace(/\/+$/, "");
  const sandboxID = requiredEnv("SANDBOX_ID");
  const capToken = requiredEnv("CAP_TOKEN");
  let outputSchemaJson = "";
  let validator: ((value: unknown) => unknown) | undefined;
  if (options.outputSchema !== undefined) {
    const normalized = normalizeOutputSchema(options.outputSchema, "delegate");
    if (!isPlainJsonObject(normalized.schema)) {
      throw new Error("delegate outputSchema must be a plain JSON object");
    }
    outputSchemaJson = JSON.stringify(normalized.schema);
    validator = normalized.validator;
  }
  const timeoutMs = options.timeoutMs && options.timeoutMs > 0 ? options.timeoutMs : undefined;
  const response = await fetch(`${baseURL}/api/runtime/sandboxes/${encodeURIComponent(sandboxID)}/delegations`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "x-capability-sandbox-token": capToken,
    },
    body: JSON.stringify({
      targetAgent,
      prompt,
      outputSchemaJson: outputSchemaJson || undefined,
      timeoutMs,
      idempotencyKey: options.idempotencyKey,
      reason: options.reason,
    }),
    signal: timeoutMs ? AbortSignal.timeout(timeoutMs + 1_000) : undefined,
  });
  const wire = (await response.json()) as DelegationWireResponse;
  let structuredResult: unknown = wire.structuredResult ?? null;
  if (validator && structuredResult !== null) {
    structuredResult = validator(structuredResult);
  }
  if (!response.ok && !wire.error) {
    wire.error = { classification: "http", message: `delegation request failed with HTTP ${response.status}` };
  }
  return {
    childRunId: wire.childRunId ?? "",
    parentRunId: wire.parentRunId ?? "",
    rootRunId: wire.rootRunId ?? "",
    delegationId: wire.delegationId ?? "",
    attempt: wire.attempt ?? 0,
    attemptRunIds: wire.attemptRunIds ?? [],
    status: wire.status ?? "",
    output: wire.output ?? "",
    resultJson: wire.resultJson ?? "",
    structuredResult: structuredResult as T | null,
    warnings: wire.warnings ?? [],
    error: wire.error ?? null,
  };
}

export async function reportDelegationTakeover(delegationId: string, options: { childRunId?: string; reason?: string } = {}): Promise<void> {
  delegationId = delegationId.trim();
  if (!delegationId) throw new Error("delegationId is required");
  const baseURL = requiredEnv("AGENT_COMPOSE_RUNTIME_BASE_URL").replace(/\/+$/, "");
  const sandboxID = requiredEnv("SANDBOX_ID");
  const capToken = requiredEnv("CAP_TOKEN");
  const response = await fetch(`${baseURL}/api/runtime/sandboxes/${encodeURIComponent(sandboxID)}/delegations/${encodeURIComponent(delegationId)}/takeover`, {
    method: "POST",
    headers: { "content-type": "application/json", "x-capability-sandbox-token": capToken },
    body: JSON.stringify(options),
  });
  if (!response.ok) throw new Error(`record delegation takeover failed with HTTP ${response.status}`);
}

function requiredEnv(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) {
    throw new Error(`required environment variable ${name} is missing`);
  }
  return value;
}
