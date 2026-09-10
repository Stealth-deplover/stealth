import { access } from "node:fs/promises";
import { spawn } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const consoleDirectory = resolve(scriptDirectory, "..");
const toolDirectory = resolve(consoleDirectory, "tools/eslint");
const eslintBinary = resolve(
  toolDirectory,
  "node_modules/eslint/bin/eslint.js",
);
const config = resolve(consoleDirectory, "eslint.config.mjs");
const npmCommand = process.platform === "win32" ? "npm.cmd" : "npm";

const run = (command, args, options) =>
  new Promise((resolvePromise, reject) => {
    const child = spawn(command, args, { stdio: "inherit", ...options });
    child.once("error", reject);
    child.once("exit", (code, signal) => {
      if (code === 0) {
        resolvePromise();
        return;
      }

      reject(
        new Error(
          `${command} exited with ${signal ? `signal ${signal}` : `code ${code}`}`,
        ),
      );
    });
  });

try {
  await access(eslintBinary);
} catch {
  await run(npmCommand, ["ci", "--ignore-scripts"], { cwd: toolDirectory });
}

await run(process.execPath, [eslintBinary, "--config", config, "."], {
  cwd: consoleDirectory,
});
