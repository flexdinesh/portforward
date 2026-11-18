# README cover

Source for [`../cover.webp`](../cover.webp): a terminal session built with the
Stageshow kit (vendored in `kit/`,
MIT, see `kit/LICENSE`). Output strings match `cmd/portforward`; update
`src/Cover.tsx` when they change.

Requires Node 20.19+, Google Chrome and `ffmpeg` with `libwebp`.

```sh
npm install
npm run dev      # live preview
npm run export   # build, render one loop, write ../cover.webp
```

`export.mjs` installs the kit's virtual clock (`?record`), steps it one frame at
a time, screenshots the stage at 2x with transparent corners, and encodes with
`ffmpeg -c:v libwebp_anim`. Set `CHROME_PATH`, `FPS` (25) or `QUALITY` (80) to
override defaults.

Font: JetBrains Mono, SIL Open Font License (`src/fonts/OFL.txt`).
