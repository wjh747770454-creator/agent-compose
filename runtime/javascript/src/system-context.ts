import { readText } from "./fs.js";

export function buildSystemContext(agentPrompt: string, mpiContext: string): string {
  const agent = agentPrompt.trim();
  if (!agent) {
    return mpiContext.trim() === "" ? "" : mpiContext;
  }

  const parts: string[] = ["## Agent Identity", "", agent];
  const mpi = mpiContext.trim();
  if (mpi) {
    parts.push("", mpi);
  }
  return parts.join("\n").trim();
}

export async function readSystemPromptFile(path?: string): Promise<string> {
  if (!path) {
    return "";
  }
  try {
    return (await readText(path)).trim();
  } catch (error) {
    if ((error as NodeJS.ErrnoException)?.code === "ENOENT") {
      return "";
    }
    throw error;
  }
}
