import { readdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
const entries = [];
for (const store of (await readdir("node_modules/.pnpm")).sort()) {
  const root = path.join("node_modules/.pnpm", store, "node_modules");
  let packages;
  try {
    packages = await readdir(root, { withFileTypes: true });
  } catch {
    continue;
  }
  for (const item of packages) {
    // Symlinks are other packages' dependencies, not duplicate license roots.
    if (!item.isDirectory()) continue;
    const dirs = item.name.startsWith("@")
      ? (await readdir(path.join(root, item.name), { withFileTypes: true }))
          .filter((v) => v.isDirectory())
          .map((v) => path.join(root, item.name, v.name))
      : [path.join(root, item.name)];
    for (const dir of dirs) {
      let pkg;
      try {
        pkg = JSON.parse(
          await readFile(path.join(dir, "package.json"), "utf8"),
        );
      } catch {
        continue;
      }
      const files = (await readdir(dir)).filter((v) =>
        /^(license|licence|copying|notice)(\.|$)/i.test(v),
      );
      if (!files.length) continue;
      const texts = await Promise.all(
        files.map((v) => readFile(path.join(dir, v), "utf8").catch(() => "")),
      );
      entries.push(`${pkg.name}@${pkg.version}\n${texts.join("\n")}`);
    }
  }
}
entries.push(
  'shadcn/ui\nCopyright (c) 2023 shadcn\n\nPermission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:\n\nThe above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.\n\nTHE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.',
);
await writeFile(
  "out/frontend-licenses.txt",
  [...new Set(entries)].join("\n\n" + "=".repeat(72) + "\n\n"),
);
