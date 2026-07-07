import { existsSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

function bundledWitnessBin() {
  const os = { darwin: "darwin", linux: "linux", win32: "windows" }[process.platform]
  const arch = { x64: "amd64", arm64: "arm64" }[process.arch]
  if (!os || !arch) return ""
  const name = `witness-${os}-${arch}${os === "windows" ? ".exe" : ""}`
  const packageRoot = path.dirname(fileURLToPath(import.meta.url))
  const bin = path.join(packageRoot, "dist", name)
  return existsSync(bin) ? bin : ""
}

const WITNESS_BIN = globalThis.WITNESS_SHIM || process.env.WITNESS_BIN || bundledWitnessBin() || "witness"

function eventType(event) {
  return String(event?.type || "")
}

function spawnWitness(args, payload) {
  if (process.env.WITNESS_WORKER === "1") return
  try {
    const proc = Bun.spawn([WITNESS_BIN, ...args], {
      stdin: payload ? new Blob([JSON.stringify(payload)]) : "ignore",
      stdout: "ignore",
      stderr: "ignore",
      env: { ...process.env, WITNESS_OPENCODE_PLUGIN: "1" },
    })
    proc.unref?.()
  } catch {
    // Plugins must never break an OpenCode session.
  }
}

function capture(event) {
  spawnWitness(["capture", "--agent", "opencode"], event)
}

function syncOpenCode() {
  spawnWitness(["import", "--agent", "opencode", "--quiet"])
}

const plugin = async () => ({
  event: async ({ event }) => {
    if (process.env.WITNESS_WORKER === "1") return
    const type = eventType(event)
    if (type.startsWith("message.updated")) {
      capture(event)
      return
    }
    if (type.startsWith("session.idle")) {
      capture(event)
      syncOpenCode()
    }
  },
})

export const Witness = plugin
export const ClaudeWitness = plugin
