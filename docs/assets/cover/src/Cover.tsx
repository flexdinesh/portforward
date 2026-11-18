import { Caret, Prompt, Stage, Window, type Act } from '../kit'
import './cover.css'

// README cover: one terminal session. `add` opens the host picker, the
// filter narrows to prod, a port is entered and the forward is added. `list`
// shows it beside an older forward through bastion that has dropped;
// `reconnect` restores it and a second `list` shows both active. `clear`
// is the loop's visible reset. Output strings match cmd/portforward.

type Step = 'add' | 'list' | 'reconnect' | 'relist' | 'clear'

type S = {
  step: Step
  add: string
  picker: boolean
  filter: string
  port: boolean
  portText: string
  added: boolean
  list: string
  listed: boolean
  reconnect: string
  reconnected: boolean
  relist: string
  relisted: boolean
  clear: string
}

const INITIAL: S = {
  step: 'add',
  add: '',
  picker: false,
  filter: '',
  port: false,
  portText: '',
  added: false,
  list: '',
  listed: false,
  reconnect: '',
  reconnected: false,
  relist: '',
  relisted: false,
  clear: '',
}

const POSTER: S = {
  ...INITIAL,
  step: 'clear',
  add: 'portforward add',
  added: true,
  list: 'portforward list',
  listed: true,
  reconnect: 'portforward reconnect',
  reconnected: true,
  relist: 'portforward list',
  relisted: true,
}

const PATH = '~/work'

// Managed forwards first, then ~/.ssh/config aliases, as the picker orders them.
const HOSTS = [
  { host: 'bastion', note: 'forward' },
  { host: 'prod', note: 'ssh config' },
  { host: 'prod-db', note: 'ssh config' },
  { host: 'staging', note: 'ssh config' },
]
const HOST_COLUMN = Math.max(...HOSTS.map((h) => h.host.length))

type Entry = { local: string; host: string; destination: string; status: string }

// tabwriter(minwidth 0, padding 2), as `portforward list` prints.
function table(entries: Entry[]): string[] {
  const rows = [['LOCAL', 'SSH HOST', 'DESTINATION', 'STATUS'], ...entries.map((e) => [e.local, e.host, e.destination, e.status])]
  const widths = [0, 1, 2].map((c) => Math.max(...rows.map((r) => r[c].length)) + 2)
  return rows.map((r) => r.map((cell, c) => (c < 3 ? cell.padEnd(widths[c]) : cell)).join(''))
}

const PROD: Entry = { local: '127.0.0.1:5432', host: 'prod', destination: 'localhost:5432', status: 'active' }
const BASTION: Entry = { local: '127.0.0.1:15432', host: 'bastion', destination: 'db.internal:5432', status: 'disconnected' }
const LIST_BEFORE = table([PROD, BASTION])
const LIST_AFTER = table([PROD, { ...BASTION, status: 'active' }])

async function script(a: Act<S>) {
  a.mark('start')
  await a.wait(900)

  // portforward add: pick a host by typing, then a port
  await a.type('add', 'portforward add', { interval: 55 })
  await a.wait(380)
  await a.set({ picker: true })
  a.mark('picker')
  await a.wait(900)
  await a.type('filter', 'pro', { interval: 240 })
  a.mark('filtered')
  await a.wait(900)
  await a.set({ picker: false, port: true })
  await a.wait(500)
  await a.type('portText', '5432', { interval: 150 })
  await a.wait(500)
  await a.set({ port: false, added: true, step: 'list' })
  a.mark('added')
  await a.wait(1000)

  // portforward list: the new forward, and one that dropped
  await a.type('list', 'portforward list', { interval: 55 })
  await a.wait(350)
  await a.set({ listed: true, step: 'reconnect' })
  a.mark('listed')
  await a.wait(2000)

  // portforward reconnect: restore it, then list again
  await a.type('reconnect', 'portforward reconnect', { interval: 55 })
  await a.wait(900)
  await a.set({ reconnected: true, step: 'relist' })
  a.mark('reconnected')
  await a.wait(1100)
  await a.type('relist', 'portforward list', { interval: 55 })
  await a.wait(350)
  await a.set({ relisted: true, step: 'clear' })
  a.mark('payoff')
  await a.wait(3200)

  // clear: the visible reset back to an empty prompt
  await a.type('clear', 'clear', { interval: 70 })
  await a.wait(350)
  await a.snap({ ...INITIAL })
  await a.wait(500)
}

function Command({ text, active }: { text: string; active: boolean }) {
  return (
    <Prompt path={PATH}>
      {text}
      {active && <Caret still />}
    </Prompt>
  )
}

function Lines({ lines }: { lines: string[] }) {
  return (
    <>
      {lines.map((line, i) => (
        <div key={i} className="pf-line">
          {line}
        </div>
      ))}
    </>
  )
}

function Picker({ filter }: { filter: string }) {
  const matches = HOSTS.filter((h) => h.host.toLowerCase().includes(filter.toLowerCase()))
  return (
    <>
      <div className="pf-line">Choose SSH host</div>
      <div className="pf-line">Filter: {filter}</div>
      {matches.map((h, i) => (
        <div key={h.host} className="pf-line" data-current={i === 0 || undefined}>
          {(i === 0 ? '> ' : '  ') + h.host.padEnd(HOST_COLUMN + 2) + h.note}
        </div>
      ))}
      <div className="pf-line dim">type to filter · ↑/↓ move · enter select · esc cancel</div>
    </>
  )
}

function PortPrompt({ value }: { value: string }) {
  return (
    <>
      <div className="pf-line">
        Local port for prod: {value}
        <Caret still />
      </div>
      <div className="pf-line dim">enter confirm · esc cancel</div>
    </>
  )
}

export default function Cover() {
  return (
    <Stage width={880} height={480} initial={INITIAL} poster={POSTER} script={script} className="pf">
      {(s) => (
        <Window chrome="terminal" title="zsh · ~/work" className="pf-term" body={{ 'data-snap': '' }}>
          <Command text={s.add} active={s.step === 'add' && !s.picker && !s.port && !s.added} />
          {s.picker && <Picker filter={s.filter} />}
          {s.port && <PortPrompt value={s.portText} />}
          {s.added && <div className="pf-line">Added 127.0.0.1:5432 → localhost:5432 through prod.</div>}

          {s.added && <Command text={s.list} active={s.step === 'list'} />}
          {s.listed && <Lines lines={LIST_BEFORE} />}

          {s.listed && <Command text={s.reconnect} active={s.step === 'reconnect'} />}
          {s.reconnected && <div className="pf-line">Reconnected 127.0.0.1:15432 → db.internal:5432 through bastion.</div>}

          {s.reconnected && <Command text={s.relist} active={s.step === 'relist'} />}
          {s.relisted && <Lines lines={LIST_AFTER} />}

          {s.relisted && <Command text={s.clear} active={s.step === 'clear'} />}
        </Window>
      )}
    </Stage>
  )
}
