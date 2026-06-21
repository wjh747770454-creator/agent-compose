import fs from "node:fs/promises";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { readMpiContext } from "../src/mpi.js";
import { buildSystemContext, readSystemPromptFile } from "../src/system-context.js";
import { withTempSession } from "./helpers.js";

describe("system-context", () => {
  it("builds agent identity section before mpi section", () => {
    const mpiFormatted = [
      "## MPI Catalog",
      "",
      "MPI catalog body",
    ].join("\n");
    const combined = buildSystemContext("Reply only in Chinese", mpiFormatted);
    expect(combined).toBe([
      "## Agent Identity",
      "",
      "Reply only in Chinese",
      "",
      "## MPI Catalog",
      "",
      "MPI catalog body",
    ].join("\n"));
  });

  it("returns agent-only content when mpi is empty", () => {
    const combined = buildSystemContext("Agent prompt", "");
    expect(combined).toBe("## Agent Identity\n\nAgent prompt");
  });

  it("returns mpi catalog unchanged when agent prompt is empty", () => {
    const mpiFormatted = [
      "## MPI Catalog",
      "",
      "The runtime provided the following Model Program Interface (MPI) catalog as high-priority context.",
      "catalog body",
      "",
    ].join("\n");
    expect(buildSystemContext("", mpiFormatted)).toBe(mpiFormatted);
  });

  it("returns empty when both sections are empty", () => {
    expect(buildSystemContext("   ", "\n")).toBe("");
  });

  it("composes mpi-only context like pre-change injection when system prompt is empty", async () => {
    await withTempSession(async (root) => {
      const stateRoot = path.join(root, "state");
      const mpiRoot = path.join(root, "runtime", "mpi");
      await fs.mkdir(mpiRoot, { recursive: true });
      await fs.writeFile(path.join(mpiRoot, "catalog.md"), "# Tools\n", "utf8");

      const mpi = await readMpiContext(stateRoot);
      const combined = buildSystemContext("", mpi.context);

      expect(combined).toBe(mpi.context);
      expect(combined).toContain("## MPI Catalog");
      expect(combined).not.toContain("## Agent Identity");
      expect(combined).not.toContain("## Capabilities (MPI)");
    });
  });

  it("reads and trims system prompt file content", async () => {
    await withTempSession(async (root) => {
      const promptPath = path.join(root, "system-prompt.txt");
      await fs.writeFile(promptPath, "  Reply only in Chinese  ", "utf8");
      await expect(readSystemPromptFile(promptPath)).resolves.toBe("Reply only in Chinese");
    });
  });

  it("returns empty for missing or undefined prompt file", async () => {
    await withTempSession(async (root) => {
      await expect(readSystemPromptFile(path.join(root, "missing.txt"))).resolves.toBe("");
      await expect(readSystemPromptFile()).resolves.toBe("");
    });
  });
});
