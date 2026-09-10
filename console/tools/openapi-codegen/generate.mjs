import { readFile, writeFile } from "node:fs/promises";
import openapiTS, { astToString } from "openapi-typescript";

const [input, output] = process.argv.slice(2);

if (!input || !output) {
  throw new Error("Usage: node generate.mjs <input> <output>");
}

const document = await readFile(input, "utf8");
const ast = await openapiTS(document, { enum: true });
await writeFile(output, astToString(ast), "utf8");
