import { createHash } from "node:crypto"
import { readFileSync } from "node:fs"
try {
  const manifest = JSON.parse(readFileSync("scripts/manifest.json", "utf8"))
  for (const name of ["emit-candidate-config.cjs", "THIRD_PARTY_LICENSES.txt"]) {
    const actual = createHash("sha256").update(readFileSync(`scripts/${name}`)).digest("hex")
    if (actual !== manifest.files[name]) throw new Error("digest")
  }
} catch {
  process.stderr.write("candidate producer distribution integrity check failed\n")
  process.exitCode = 1
}
