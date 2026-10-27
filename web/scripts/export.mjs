import { readdir, readFile, writeFile, cp, rm } from "node:fs/promises";
import { createHash } from "node:crypto";
import path from "node:path";
// Next emits a handful of bootstrap scripts inline. Hash the actual exported
// bytes so Go can serve them under CSP without unsafe-inline or unsafe-eval.
const hashes = new Set();
async function visit(dir) {
  for (const file of await readdir(dir, { withFileTypes: true })) {
    const p = path.join(dir, file.name);
    if (file.isDirectory()) await visit(p);
    else if (p.endsWith(".html")) {
      const html = await readFile(p, "utf8");
      for (const m of html.matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script>/g)) {
        if (!/\bsrc=/.test(m[1]))
          hashes.add(
            "'sha256-" +
              createHash("sha256").update(m[2]).digest("base64") +
              "'",
          );
      }
    }
  }
}
await visit("out");
const dest = "../internal/httpapi/static";
await rm(dest, { recursive: true, force: true });
await cp("out", dest, { recursive: true });
await writeFile(path.join(dest, "script-hashes.txt"), [...hashes].join(" "));
console.log("Exported admin assets and CSP script hashes.");
