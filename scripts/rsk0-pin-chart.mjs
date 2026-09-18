import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync, existsSync, realpathSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { createHash } from 'node:crypto'
const sha = /^[a-f0-9]{40}$/
function check(ok) { if (!ok) throw Error('chart pin refused') }
function git(...args) { return execFileSync('git', args, { encoding: 'utf8', timeout: 30000, maxBuffer: 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe'] }).trimEnd() }
function number(value) { check(/^[1-9][0-9]*$/.test(value ?? '') && Number.isSafeInteger(Number(value))); return Number(value) }
/** Deliberately restricted values syntax: one top-level image block and one plain tag. */
export function pinTag(text, value) {
  check(sha.test(value) && Buffer.byteLength(text) <= 65536 && !text.includes('\r'))
  const lines = text.split('\n'), roots = lines.map((line, i) => line === 'image:' ? i : -1).filter(i => i >= 0)
  check(roots.length === 1 && lines.filter(line => /^(?:image\s*:|[\"']image[\"']\s*:)/.test(line)).length === 1); let end = roots[0] + 1
  while (end < lines.length && (!lines[end].trim() || /^\s|^#/.test(lines[end]))) end++
  const keys = new Set()
  for (const line of lines.slice(roots[0] + 1, end)) {
    if (!line.trim() || /^\s*#/.test(line)) continue
    const match = /^  ([A-Za-z][A-Za-z0-9_]*): [A-Za-z0-9_./:@-]+$/.exec(line)
    check(match && !keys.has(match[1])); keys.add(match[1])
  }
  const tags = []
  for (let i = roots[0] + 1; i < end; i++) if (/^  tag:/.test(lines[i])) tags.push(i)
  check(tags.length === 1 && /^  tag: [A-Za-z0-9_.-]+$/.test(lines[tags[0]]))
  lines[tags[0]] = `  tag: ${value}`
  return lines.join('\n')
}
export function main(env = process.env) {
  const repository = env.GITHUB_REPOSITORY, sourceCommit = env.GITHUB_SHA, branch = 'main', path = 'chart/values.yaml'
  check(env.GITHUB_ACTIONS === 'true' && /^[A-Za-z0-9_-]+\/[A-Za-z0-9_.-]+$/.test(repository ?? '') && sha.test(sourceCommit ?? '') &&
    env.GITHUB_EVENT_NAME === 'push' && env.GITHUB_REF === 'refs/heads/main' && env.GITHUB_SERVER_URL === 'https://github.com' &&
    env.GITHUB_WORKFLOW_REF === `${repository}/.github/workflows/ci.yml@refs/heads/main` && env.GITHUB_WORKFLOW_SHA === sourceCommit)
  const runId = number(env.GITHUB_RUN_ID), runAttempt = number(env.GITHUB_RUN_ATTEMPT), repositoryId = number(env.GITHUB_REPOSITORY_ID)
  check(!existsSync('chart-commit.json') && git('status', '--porcelain', '--untracked-files=no') === '')
  check([`https://github.com/${repository}`, `https://github.com/${repository}.git`].includes(git('config', '--get', 'remote.origin.url')))
  check(git('rev-parse', 'HEAD') === sourceCommit)
  git('fetch', '--no-tags', 'origin', 'refs/heads/main:refs/remotes/origin/main')
  // No rebase/retry onto a newer release: an older build must not overwrite it.
  check(git('rev-parse', 'refs/remotes/origin/main') === sourceCommit)
  const before = readFileSync(path, 'utf8'), after = pinTag(before, sourceCommit)
  if (before === after) return { status: 'no_change', receiptCreated: false }
  const beforeBlob = git('rev-parse', `${sourceCommit}:${path}`)
  writeFileSync(path, after)
  git('add', '--', path)
  check(git('diff', '--cached', '--name-only') === path)
  git('-c', 'user.name=rsk0-ci', '-c', 'user.email=ci@rsk0.app', 'commit', '-m', `ci: pin chart image.tag to ${sourceCommit} [skip ci]`)
  const chartCommit = git('rev-parse', 'HEAD'), afterBlob = git('rev-parse', `HEAD:${path}`)
  check(sha.test(chartCommit) && git('rev-parse', 'HEAD^') === sourceCommit)
  git('push', 'origin', `${chartCommit}:refs/heads/main`)
  check(git('ls-remote', 'origin', 'refs/heads/main').split(/\s/)[0] === chartCommit)
  const receipt = { version: 'rsk0-chart-commit/1', repository, repositoryId, runId, runAttempt, workflowPath: '.github/workflows/ci.yml',
    sourceCommit, chartCommit, parentCommit: sourceCommit, branch, path, beforeBlob, afterBlob,
    producerSha256: createHash('sha256').update(readFileSync(new URL(import.meta.url))).digest('hex') }
  writeFileSync('chart-commit.json', JSON.stringify(receipt, null, 2) + '\n', { flag: 'wx', mode: 0o600 })
  if (env.GITHUB_OUTPUT) writeFileSync(env.GITHUB_OUTPUT, 'receipt_created=true\n', { flag: 'a' })
  return { status: 'pushed', receiptCreated: true, chartCommit }
}
if (process.argv[1] && fileURLToPath(import.meta.url) === realpathSync(process.argv[1])) {
  try { if (process.argv.length !== 2) throw Error('arguments'); process.stdout.write(JSON.stringify(main()) + '\n') }
  catch { process.stderr.write('Chart pin refused: verify CI identity, current main, clean checkout, supported values and push result\n'); process.exitCode = 1 }
}
