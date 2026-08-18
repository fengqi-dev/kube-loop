import { spawn, spawnSync } from "node:child_process"
import { existsSync } from "node:fs"
import { fileURLToPath } from "node:url"
import path from "node:path"
import process from "node:process"

const frontendDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")
const rootDirectory = path.resolve(frontendDirectory, "..")
const desktopDirectory = path.join(frontendDirectory, "apps", "desktop")
const developmentBundle = path.join(rootDirectory, "build", "bin", "kube-loop.app")
const installedBundle = "/Applications/KubeLoop.app"
const launchServices =
  "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

function run(command, args, cwd = rootDirectory) {
  const result = spawnSync(command, args, { cwd, stdio: "inherit" })
  if (result.error) {
    throw result.error
  }
  if (result.status !== 0) {
    throw new Error(`${command} exited with status ${result.status}`)
  }
}

function launchServicesCommand(args) {
  const result = spawnSync(launchServices, args, { stdio: "pipe", encoding: "utf8" })
  if (result.error) {
    throw result.error
  }
  if (result.status !== 0) {
    const detail = result.stderr.trim() || result.stdout.trim() || `status ${result.status}`
    throw new Error(`lsregister ${args[0]} failed: ${detail}`)
  }
}

let callbackRegistered = false
let registrationTimer
let vite
let shuttingDown = false

function registerDevelopmentCallback() {
  if (process.platform !== "darwin" || callbackRegistered || !existsSync(developmentBundle)) {
    return
  }
  if (existsSync(installedBundle)) {
    launchServicesCommand(["-u", installedBundle])
  }
  launchServicesCommand(["-f", developmentBundle])
  callbackRegistered = true
  console.log(`==> Registered kubeloop:// callback for ${developmentBundle}`)
}

function restoreInstalledCallback() {
  if (process.platform !== "darwin" || !callbackRegistered) {
    return
  }
  launchServicesCommand(["-u", developmentBundle])
  if (existsSync(installedBundle)) {
    launchServicesCommand(["-f", installedBundle])
    console.log(`==> Restored kubeloop:// callback for ${installedBundle}`)
  }
  callbackRegistered = false
}

function shutdown(signal, exitCode = 0) {
  if (shuttingDown) {
    return
  }
  shuttingDown = true
  clearInterval(registrationTimer)
  if (vite && !vite.killed) {
    vite.kill(signal)
  }
  try {
    restoreInstalledCallback()
  } catch (error) {
    console.error(`Failed to restore the installed KubeLoop callback: ${error.message}`)
    exitCode = 1
  }
  process.exit(exitCode)
}

try {
  run("npm", ["--prefix", frontendDirectory, "run", "build:admin"])
  run("go", ["run", "./build/gateway-dev.go"])

  vite = spawn("npm", ["exec", "--", "vite"], {
    cwd: desktopDirectory,
    stdio: "inherit",
  })
  vite.on("error", (error) => {
    console.error(`Failed to start Vite: ${error.message}`)
    shutdown("SIGTERM", 1)
  })
  vite.on("exit", (code, signal) => shutdown(signal || "SIGTERM", signal ? 0 : (code ?? 1)))

  if (process.platform === "darwin") {
    registrationTimer = setInterval(() => {
      try {
        registerDevelopmentCallback()
        if (callbackRegistered) {
          clearInterval(registrationTimer)
        }
      } catch (error) {
        console.error(`Failed to register the Wails development callback: ${error.message}`)
        shutdown("SIGTERM", 1)
      }
    }, 200)
  }
} catch (error) {
  console.error(`Failed to prepare Wails development mode: ${error.message}`)
  shutdown("SIGTERM", 1)
}

process.on("SIGINT", () => shutdown("SIGINT", 0))
process.on("SIGTERM", () => shutdown("SIGTERM", 0))
