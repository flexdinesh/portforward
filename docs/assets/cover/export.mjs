// Render one loop of the built cover (dist/) to ../cover.webp.
// Steps the kit's virtual clock frame by frame, screenshots the stage at 2x
// with transparent corners, then encodes an animated webp with ffmpeg.
//
//   npm run export            (vite build && node export.mjs)
//   CHROME_PATH=/path/to/chrome FPS=25 QUALITY=80 node export.mjs

import { spawnSync } from 'node:child_process'
import { mkdirSync, rmSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'
import { preview } from 'vite'

const root = dirname(fileURLToPath(import.meta.url))
const frames = resolve(root, 'frames')
const out = resolve(root, '..', 'cover.webp')
const fps = Number(process.env.FPS ?? 25)
const quality = Number(process.env.QUALITY ?? 80)
const limit = fps * 40

rmSync(frames, { recursive: true, force: true })
mkdirSync(frames, { recursive: true })

const server = await preview({ root, preview: { port: 0, host: '127.0.0.1' } })
const url = server.resolvedUrls?.local[0]
if (!url) throw new Error('vite preview did not report a URL')

const executablePath = process.env.CHROME_PATH
const browser = await chromium.launch(executablePath ? { executablePath } : { channel: 'chrome' })
try {
  const page = await browser.newPage({ viewport: { width: 1000, height: 600 }, deviceScaleFactor: 2 })
  await page.goto(`${url}?record&fps=${fps}`)
  // Poll on a real interval: the page's requestAnimationFrame only runs when stepped.
  await page.waitForFunction(() => window.__stageshow?.clock && document.querySelector('.ss-stage'), null, { polling: 100 })
  await page.evaluate(() => document.fonts.ready)
  const stage = page.locator('.ss-stage')
  const mark = () => stage.getAttribute('data-mark')
  const step = () => page.evaluate(() => window.__stageshow.clock.step(1))

  // Run until the script starts, then record until it starts again.
  for (let i = 0; (await mark()) !== 'start'; i++) {
    if (i > limit) throw new Error('script never started')
    await step()
  }
  let count = 0
  let seenPayoff = false
  for (;;) {
    await stage.screenshot({ path: resolve(frames, `${String(count).padStart(5, '0')}.png`), omitBackground: true, animations: 'allow' })
    count++
    await step()
    const now = await mark()
    if (now === 'payoff') seenPayoff = true
    if (seenPayoff && now === 'start') break
    if (count > limit) throw new Error('loop did not end within 40s')
  }
  console.log(`captured ${count} frames (${(count / fps).toFixed(1)}s at ${fps} fps)`)
} finally {
  await browser.close()
  await new Promise((done) => server.httpServer.close(done))
}

const ffmpeg = spawnSync('ffmpeg', [
  '-y', '-loglevel', 'error',
  '-framerate', String(fps),
  '-i', resolve(frames, '%05d.png'),
  '-c:v', 'libwebp_anim', '-pix_fmt', 'yuva420p',
  '-quality', String(quality), '-compression_level', '6',
  '-loop', '0',
  out,
], { stdio: 'inherit' })
if (ffmpeg.status !== 0) process.exit(ffmpeg.status ?? 1)
console.log(`wrote ${out}`)
