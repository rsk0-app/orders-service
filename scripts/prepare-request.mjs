// Operator supplies public target and observed Secret identity, never Secret values.
import { readFileSync, writeFileSync } from "node:fs"
try {
  if (process.argv.length !== 3) throw new Error("usage")
  const target = JSON.parse(readFileSync(process.argv[2], "utf8"))
  const fields = ["organisationId", "serviceId", "environmentId", "cluster", "namespace", "secretName", "secretVersion"]
  if (Object.keys(target).length !== fields.length || fields.some(k => typeof target[k] !== "string" || !target[k])) throw new Error("target")
  if (!/^k8s:[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}:[1-9][0-9]*$/.test(target.secretVersion)) throw new Error("secret version")
  const request = { organisationId: target.organisationId, serviceId: target.serviceId, environmentId: target.environmentId,
    cluster: target.cluster, namespace: target.namespace,
    commitSha: process.env.GITHUB_SHA, imageRepository: process.env.RSK0_IMAGE_REPOSITORY, artifactDigest: process.env.RSK0_IMAGE_DIGEST,
    declaredDependencies: [], configMaps: [], secrets: [{ name: target.secretName, version: target.secretVersion }] }
  // The canonical producer validates the complete request/render before artifact publication.
  writeFileSync("candidate-request.json", JSON.stringify(request) + "\n", { flag: "wx", mode: 0o600 })
  writeFileSync("candidate-values.json", JSON.stringify({ image: { repository: request.imageRepository, digest: request.artifactDigest }, database: { secretName: target.secretName } }) + "\n", { flag: "wx", mode: 0o600 })
} catch {
  process.stderr.write("candidate request requires explicit target and Kubernetes Secret identity\n")
  process.exitCode = 1
}
