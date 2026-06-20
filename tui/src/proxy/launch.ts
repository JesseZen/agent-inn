import { spawn } from "node:child_process"
import { platform } from "node:os"

export type ProxyLaunchOptions = {
  executable?: string
  workerPort: number
  profile?: string
  workspace?: string
  addDirs?: string[]
  model?: string
}

function shellQuote(value: string) {
  if (value === "") return "''"
  return "'" + value.replace(/'/g, `'\\''`) + "'"
}

export function createProxyLaunchCommand(opts: ProxyLaunchOptions) {
  const cmd = [opts.executable || "codex-proxy", "launch", "--worker", String(opts.workerPort)]
  if (opts.profile) {
    cmd.push("--profile", opts.profile)
  }
  if (opts.workspace) {
    cmd.push("--cd", opts.workspace)
  }
  for (const dir of opts.addDirs ?? []) {
    if (dir) cmd.push("--add-dir", dir)
  }
  if (opts.model) {
    cmd.push("--model", opts.model)
  }
  return cmd
}

export function renderProxyLaunchCommand(cmd: string[]) {
  return cmd.map(shellQuote).join(" ")
}

export async function launchProxySession(opts: ProxyLaunchOptions) {
  const executable = opts.executable || "codex-proxy"
  const args = ["launch", "--worker", String(opts.workerPort)]
  if (opts.profile) {
    args.push("--profile", opts.profile)
  }
  if (opts.workspace) {
    args.push("--cd", opts.workspace)
  }
  for (const dir of opts.addDirs ?? []) {
    if (dir) args.push("--add-dir", dir)
  }
  if (opts.model) {
    args.push("--model", opts.model)
  }
  if (platform() === "darwin") {
    const command = renderProxyLaunchCommand([executable, ...args])
    const escaped = command.replace(/\\/g, "\\\\").replace(/"/g, '\\"')
    await new Promise<void>((resolve, reject) => {
      const child = spawn("osascript", ["-e", `tell application "Terminal" to do script "${escaped}"`], {
        stdio: "ignore",
      })
      child.on("error", reject)
      child.on("exit", (code) => {
        if (code === 0) return resolve()
        reject(new Error(`osascript exited with code ${code}`))
      })
    })
    return true
  }

  const child = spawn(executable, args, {
    detached: true,
    stdio: "ignore",
  })
  child.unref()
  return true
}
