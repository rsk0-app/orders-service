// Run after docker/build-push-action, before uploading candidate-build.json.
// Credentials/configuration are never serialized; image digest must come from the build output.
import { writeFileSync } from 'node:fs'
const payload = {
  schemaVersion: 1,
  repository: process.env.GITHUB_REPOSITORY,
  commitSha: process.env.GITHUB_SHA,
  imageRepository: process.env.RSK0_IMAGE_REPOSITORY,
  imageDigest: process.env.RSK0_IMAGE_DIGEST,
  runId: Number(process.env.GITHUB_RUN_ID),
  runAttempt: Number(process.env.GITHUB_RUN_ATTEMPT),
}
if (process.env.GITHUB_EVENT_NAME !== 'push' || process.env.GITHUB_REF !== 'refs/heads/main' ||
    !/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(payload.repository ?? '') ||
    !/^[a-f0-9]{40}$/.test(payload.commitSha ?? '') ||
    !/^ghcr\.io\/[a-z0-9_.-]+\/[a-z0-9_.-]+$/.test(payload.imageRepository ?? '') ||
    !/^sha256:[a-f0-9]{64}$/.test(payload.imageDigest ?? '') ||
    !Number.isSafeInteger(payload.runId) || payload.runId < 1 ||
    !Number.isSafeInteger(payload.runAttempt) || payload.runAttempt < 1) {
  throw new Error('candidate build requires main push context and immutable build digest')
}
writeFileSync('candidate-build.json', JSON.stringify(payload) + '\n', { mode: 0o600, flag: 'wx' })
