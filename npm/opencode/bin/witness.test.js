import assert from "node:assert/strict"
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises"
import os from "node:os"
import path from "node:path"
import test from "node:test"
import { pathToFileURL } from "node:url"

const sourcePath = new URL("./witness.js", import.meta.url)
const source = (await readFile(sourcePath, "utf8"))
  .replace('import { spawnSync } from "node:child_process"', 'const { spawnSync } = globalThis.__witnessCliHarness.childProcess')
  .replace('import { existsSync } from "node:fs"', 'const { existsSync } = globalThis.__witnessCliHarness.fs')
  .replace('import { modelDir } from "./model.js"', 'const { modelDir } = globalThis.__witnessCliHarness.model')

async function runCLI(argv, harness) {
  const dir = await mkdtemp(path.join(os.tmpdir(), "witness-opencode-cli-"))
  await mkdir(path.join(dir, "bin"), { recursive: true })
  const script = path.join(dir, "bin", "witness.mjs")
  await writeFile(script, source)

  const previous = {
    argv: process.argv,
    exit: process.exit,
    error: console.error,
    harness: globalThis.__witnessCliHarness,
  }
  const errors = []
  const sentinel = new Error("exit")
  sentinel.name = "WitnessCLIExit"

  globalThis.__witnessCliHarness = harness
  process.argv = argv
  process.exit = (code) => {
    sentinel.code = code
    throw sentinel
  }
  console.error = (msg) => {
    errors.push(String(msg))
  }

  try {
    await import(`${pathToFileURL(script).href}?t=${Date.now()}-${Math.random()}`)
    return { code: 0, errors, spawnCalls: harness.spawnCalls || [] }
  } catch (err) {
    if (err === sentinel) return { code: sentinel.code, errors, spawnCalls: harness.spawnCalls || [] }
    throw err
  } finally {
    process.argv = previous.argv
    process.exit = previous.exit
    console.error = previous.error
    globalThis.__witnessCliHarness = previous.harness
    await rm(dir, { recursive: true, force: true })
  }
}

test("npm CLI gives a clear install/uninstall error before looking for bundled binaries", async () => {
  const harness = {
    fs: {
      existsSync() {
        throw new Error("should not probe dist for install/uninstall")
      },
    },
    model: {
      modelDir() {
        return "/assets/e5-small"
      },
    },
    childProcess: {
      spawnSync() {
        throw new Error("should not spawn install/uninstall")
      },
    },
  }
  const result = await runCLI([process.execPath, "witness", "install", "opencode"], harness)
  assert.equal(result.code, 1)
  assert.match(result.errors[0], /source-checkout commands/)
})

test("npm CLI still forwards non-install commands to the bundled binary with witness env defaults", async () => {
  const spawnCalls = []
  const harness = {
    spawnCalls,
    fs: {
      existsSync() {
        return true
      },
    },
    model: {
      modelDir() {
        return "/assets/e5-small"
      },
    },
    childProcess: {
      spawnSync(bin, args, options) {
        spawnCalls.push({ bin, args, options })
        return { status: 0 }
      },
    },
  }
  const result = await runCLI([process.execPath, "witness", "profile"], harness)
  assert.equal(result.code, 0)
  assert.equal(spawnCalls.length, 1)
  assert.deepEqual(spawnCalls[0].args, ["profile"])
  assert.equal(spawnCalls[0].options.env.WITNESS_ASSETS, "/assets/e5-small")
  assert.equal(spawnCalls[0].options.env.WITNESS_RUNNER, "opencode")
  assert.equal(spawnCalls[0].options.env.WITNESS_NPM_PACKAGE, "1")
})
