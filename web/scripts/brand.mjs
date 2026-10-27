import { mkdir, copyFile } from "node:fs/promises";

// Copy approved masters without changing their pixels or transparency.
const source = new URL("../../assets/brand/", import.meta.url);
const destination = new URL("../public/brand/", import.meta.url);
await mkdir(destination, { recursive: true });
for (const name of [
  "whatsapp-mcp-icon.png",
  "whatsapp-mcp-logo-transparent.png",
]) {
  await copyFile(new URL(name, source), new URL(name, destination));
}
