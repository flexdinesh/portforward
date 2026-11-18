import { installVirtualClock } from '../kit/clock'

// ?record installs the stepped clock before the scene loads, so export.mjs
// advances time one frame at a time and every render is identical.
async function boot() {
  const params = new URLSearchParams(location.search)
  if (params.has('record')) installVirtualClock({ fps: Number(params.get('fps') ?? 25), epoch: 0 })
  const root = document.getElementById('root')
  if (!root) throw new Error('missing #root')
  const [{ StrictMode, createElement }, { createRoot }, { default: Cover }] = await Promise.all([
    import('react'),
    import('react-dom/client'),
    import('./Cover'),
  ])
  createRoot(root).render(createElement(StrictMode, null, createElement(Cover)))
}

void boot()
