import React, { useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import {
  BarChart3,
  History,
  LogIn,
  LogOut,
  Moon,
  RefreshCw,
  Search,
  Settings,
  Sun,
  UserPlus,
} from 'lucide-react'
import './styles.css'

const api = {
  async request(path, options = {}) {
    const res = await fetch(path, {
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
      ...options,
    })
    if (res.status === 401) {
      const err = new Error('unauthorized')
      err.status = 401
      throw err
    }
    if (!res.ok) {
      let message = `${res.status} ${res.statusText}`
      try {
        const body = await res.json()
        message = body?.error?.message || message
      } catch {}
      throw new Error(message)
    }
    return res.json()
  },
  me: () => api.request('/api/auth/me'),
  login: (email, password) => api.request('/api/auth/login', { method: 'POST', body: JSON.stringify({ email, password }) }),
  logout: () => api.request('/api/auth/logout', { method: 'POST' }),
  top: (limit = 20) => api.request(`/api/top?limit=${limit}`),
  health: () => api.request('/api/health'),
  refresh: () => api.request('/api/refresh', { method: 'POST' }),
  stats: (days) => api.request(`/api/stats?days=${days}`),
  snapshots: () => api.request('/api/snapshots'),
  propList: () => api.request('/api/prop-list'),
  savePropList: (props) => api.request('/api/prop-list', { method: 'PUT', body: JSON.stringify(props) }),
  settings: () => api.request('/api/settings'),
  saveSettings: (overrides) => api.request('/api/settings', { method: 'PUT', body: JSON.stringify({ overrides }) }),
}

function App() {
  const [path, setPath] = useState(window.location.pathname)
  const [user, setUser] = useState(null)
  const [checked, setChecked] = useState(false)
  const [theme, setTheme] = useState(() => localStorage.getItem('theme') || 'system')

  useEffect(() => {
    const onPop = () => setPath(window.location.pathname)
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  useEffect(() => {
    api.me().then(setUser).catch(() => setUser(null)).finally(() => setChecked(true))
  }, [])

  useEffect(() => {
    localStorage.setItem('theme', theme)
    const dark = theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
    document.documentElement.classList.toggle('dark', dark)
  }, [theme])

  const nav = (next) => {
    window.history.pushState({}, '', next)
    setPath(next)
  }

  const onUnauthorized = () => {
    setUser(null)
    nav('/auth')
  }

  if (!checked) return <ShellLoading />
  if (path === '/') return <Landing user={user} nav={nav} theme={theme} setTheme={setTheme} />
  if (path === '/auth') return <AuthPage user={user} setUser={setUser} nav={nav} theme={theme} setTheme={setTheme} />
  if (path.startsWith('/app')) {
    if (!user) return <Redirect to="/auth" nav={nav} />
    return <PrivateApp path={path} nav={nav} user={user} setUser={setUser} onUnauthorized={onUnauthorized} theme={theme} setTheme={setTheme} />
  }
  return <Redirect to="/" nav={nav} />
}

function Redirect({ to, nav }) {
  useEffect(() => nav(to), [to])
  return null
}

function ShellLoading() {
  return <div className="app"><div className="page"><div className="skel wide" /></div></div>
}

function Landing({ user, nav, theme, setTheme }) {
  return (
    <main className="landing">
      <div className="landing-bg" />
      <div className="landing-overlay" />
      <header className="landing-nav">
        <div className="brand light">Screener</div>
        <div className="fx ac gap8">
          <ThemeButton theme={theme} setTheme={setTheme} light />
          {user ? (
            <button className="btn light" onClick={() => nav('/app/top')}>Open app</button>
          ) : (
            <>
              <button className="btn light" onClick={() => nav('/auth')}><LogIn size={16} />Sign in</button>
              <button className="btn btn-primary" onClick={() => nav('/auth')}><UserPlus size={16} />Register</button>
            </>
          )}
        </div>
      </header>
      <section className="landing-copy">
        <div className="landing-kicker">Binance / Bybit / Futures</div>
        <h1>Screener</h1>
        <p>Private futures scalp assistant: ranks live Binance and Bybit instruments by movement, turnover, BTC correlation, inplay state, and prop-list coverage.</p>
        <div className="landing-actions">
          <button className="btn btn-primary big" onClick={() => nav(user ? '/app/top' : '/auth')}>View top</button>
          <button className="btn light big" onClick={() => nav('/auth')}>Sign in or register</button>
        </div>
      </section>
    </main>
  )
}

function AuthPage({ user, setUser, nav, theme, setTheme }) {
  const [mode, setMode] = useState('login')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (user) nav('/app/top')
  }, [user])

  const submit = async (event) => {
    event.preventDefault()
    if (mode !== 'login') return
    setLoading(true)
    setError('')
    try {
      const me = await api.login(email, password)
      setUser(me)
      nav('/app/top')
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="app auth-shell">
      <header className="auth-top">
        <button className="brand as-button" onClick={() => nav('/')}>Screener</button>
        <ThemeButton theme={theme} setTheme={setTheme} />
      </header>
      <form className="auth-panel" onSubmit={submit}>
        <div className="seg">
          <button type="button" className={mode === 'login' ? 'on' : ''} onClick={() => setMode('login')}>Sign in</button>
          <button type="button" className={mode === 'register' ? 'on' : ''} onClick={() => setMode('register')}>Register</button>
        </div>
        <label className="field">
          <span>Email</span>
          <input className="input" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" />
        </label>
        <label className="field">
          <span>Password</span>
          <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
        </label>
        {error && <div className="banner compact">{error}</div>}
        <button className="btn btn-primary full" disabled={loading || mode !== 'login'}>
          {loading && <span className="spinner" />} {mode === 'login' ? 'Sign in' : 'Invite only'}
        </button>
      </form>
    </div>
  )
}

function PrivateApp({ path, nav, user, setUser, onUnauthorized, theme, setTheme }) {
  const current = path.includes('/history') ? 'history' : path.includes('/settings') ? 'settings' : 'top'
  const logout = async () => {
    try { await api.logout() } catch {}
    setUser(null)
    nav('/')
  }
  return (
    <div className="app">
      <div className="topbar">
        <div className="fx ac gap24">
          <div className="brand">Screener</div>
          <div className="fx gap4">
            <button className={`tab ${current === 'top' ? 'on' : ''}`} onClick={() => nav('/app/top')}><BarChart3 size={15} />Top</button>
            <button className={`tab ${current === 'history' ? 'on' : ''}`} onClick={() => nav('/app/history')}><History size={15} />History</button>
            <button className={`tab ${current === 'settings' ? 'on' : ''}`} onClick={() => nav('/app/settings')}><Settings size={15} />Settings</button>
          </div>
        </div>
        <div className="fx ac gap12">
          <span className="updated mono">{user.email}</span>
          <ThemeButton theme={theme} setTheme={setTheme} />
          <button className="themebtn" onClick={logout} title="Logout"><LogOut size={16} /></button>
        </div>
      </div>
      {current === 'top' && <TopPage onUnauthorized={onUnauthorized} />}
      {current === 'history' && <HistoryPage onUnauthorized={onUnauthorized} />}
      {current === 'settings' && <SettingsPage onUnauthorized={onUnauthorized} />}
    </div>
  )
}

function TopPage({ onUnauthorized }) {
  const [rows, setRows] = useState([])
  const [health, setHealth] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [search, setSearch] = useState('')
  const [exchange, setExchange] = useState('all')
  const [onlyProp, setOnlyProp] = useState(false)
  const [expanded, setExpanded] = useState(null)

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const [top, h] = await Promise.all([api.top(20), api.health()])
      setRows(top || [])
      setHealth(h)
    } catch (err) {
      if (err.status === 401) onUnauthorized()
      else setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  const refresh = async () => {
    setLoading(true)
    try {
      await api.refresh()
      await load()
    } catch (err) {
      if (err.status === 401) onUnauthorized()
      else setError(err.message)
      setLoading(false)
    }
  }

  const filtered = rows.filter((row) => {
    const symbol = displaySymbol(row.instrument)
    if (search && !symbol.includes(search.toUpperCase())) return false
    if (onlyProp && !row.in_prop_list) return false
    if (exchange === 'binance' && !row.instrument.on_binance) return false
    if (exchange === 'bybit' && !row.instrument.on_bybit) return false
    return true
  })

  return (
    <div className="page">
      <div className="fx ac gap12 wrap">
        <button className="btn btn-primary" onClick={refresh}>{loading && <span className="spinner" />}<RefreshCw size={16} />Refresh now</button>
        <label className="searchbox"><Search size={15} /><input placeholder="Search ticker" value={search} onChange={(e) => setSearch(e.target.value)} /></label>
        <select className="select" value={exchange} onChange={(e) => setExchange(e.target.value)}>
          <option value="all">All exchanges</option>
          <option value="binance">Binance</option>
          <option value="bybit">Bybit</option>
        </select>
        <label className="chk fx ac gap6"><input type="checkbox" checked={onlyProp} onChange={(e) => setOnlyProp(e.target.checked)} />prop only</label>
        <span className="updated mono">{health?.time ? `updated ${new Date(health.time).toLocaleTimeString()}` : ''}</span>
      </div>
      <div className="cards">
        <SummaryCard label="Rows" value={rows.length} />
        <SummaryCard label="Prop hits" value={`${rows.filter((r) => r.in_prop_list).length} / ${rows.length || 20}`} tone="pos" />
        <SummaryCard label="Inplay" value={rows.filter((r) => r.is_inplay).length} />
        <SummaryCard label="Bybit only" value={rows.filter((r) => r.bybit_only).length} />
      </div>
      {error && <div className="banner">{error}</div>}
      <div className="tblwrap">
        <table className="tbl">
          <thead><tr><th>#</th><th>Ticker</th><th className="hide-sm">Exchanges</th><th className="ta-r">Price</th><th className="ta-r">Turnover</th><th className="ta-r">Range</th><th className="ta-r hide-sm">Corr</th><th className="ta-r">Score</th><th className="ta-c">Prop</th></tr></thead>
          <tbody>
            {loading && rows.length === 0 ? Array.from({ length: 8 }).map((_, i) => <tr key={i}><td colSpan="9"><div className="skel" /></td></tr>) : filtered.map((row) => {
              const symbol = displaySymbol(row.instrument)
              const open = expanded === symbol
              return (
                <React.Fragment key={symbol}>
                  <tr className="rowmain" onClick={() => setExpanded(open ? null : symbol)}>
                    <td className="mono dim">{row.rank}</td>
                    <td className="fw6">{symbol}</td>
                    <td className="hide-sm"><span className="fx gap4">{row.instrument.on_binance && <span className="badge">BN</span>}{row.instrument.on_bybit && <span className="badge">BB</span>}{row.bybit_only && <span className="badge warn">NEW</span>}</span></td>
                    <td className="mono ta-r">{fmtPrice(row.price)}</td>
                    <td className="mono ta-r">{fmtMoney(row.turnover_usd)}</td>
                    <td className="mono ta-r">{fmtPct(row.day_range_pct)}</td>
                    <td className={`mono ta-r hide-sm ${row.high_corr ? 'neg' : ''}`}>{row.btc_corr_7d == null ? 'n/a' : row.btc_corr_7d.toFixed(2)}</td>
                    <td className="mono ta-r score">{Number(row.score || 0).toFixed(4)}</td>
                    <td className="ta-c">{row.in_prop_list ? <span className="badge-prop">yes</span> : <span className="dash">-</span>}</td>
                  </tr>
                  {open && <tr className="detailrow"><td colSpan="9"><div className="detail"><div className="metrics fx gap24 wrap mono"><span><span className="dim">change</span> {fmtPct(row.change_pct_24h)}</span><span><span className="dim">funding</span> {fmtPct(row.funding_rate * 100)}</span><span><span className="dim">tick</span> {Number(row.relative_tick_pct || 0).toFixed(4)}%</span><span><span className="dim">inplay</span> {row.is_inplay ? 'yes' : 'no'}</span></div></div></td></tr>}
                </React.Fragment>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function HistoryPage({ onUnauthorized }) {
  const [days, setDays] = useState(7)
  const [stats, setStats] = useState(null)
  const [snaps, setSnaps] = useState([])
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all([api.stats(days), api.snapshots()])
      .then(([s, list]) => { setStats(s); setSnaps(list || []) })
      .catch((err) => err.status === 401 ? onUnauthorized() : setError(err.message))
  }, [days])

  const frequency = stats?.Frequency || []
  const max = Math.max(...frequency.map((f) => f.Count), 1)
  return (
    <div className="page">
      <div className="fx ac gap12 wrap">
        <select className="select" value={days} onChange={(e) => setDays(Number(e.target.value))}>
          <option value="7">7 days</option>
          <option value="14">14 days</option>
          <option value="30">30 days</option>
        </select>
      </div>
      {error && <div className="banner">{error}</div>}
      <div className="section" style={{ marginTop: 16 }}>
        <div className="sec-h">Top-20 frequency</div>
        <div className="col gap10" style={{ marginTop: 14 }}>
          {frequency.slice(0, 12).map((f) => <div className="barrow fx ac gap12" key={f.Symbol}><span className="barlabel mono">{f.Symbol}</span><div className="bartrack f1"><div className="barfill" style={{ width: `${Math.max(8, f.Count / max * 100)}%` }} /></div><span className="barnum mono">{f.Count}</span></div>)}
          {!frequency.length && <div className="dim">History appears after the first saved snapshots.</div>}
        </div>
      </div>
      <div className="cards tight">
        <SummaryCard label="Avg prop hits" value={Number(stats?.AveragePropTop || 0).toFixed(1)} />
        <SummaryCard label="Period leaders" value={(stats?.Newcomers || []).slice(0, 3).join(', ') || '-'} />
        <SummaryCard label="Snapshots" value={snaps.length} />
      </div>
      <div className="tblwrap">
        <table className="tbl">
          <thead><tr><th>Date</th><th>Top-3</th><th className="ta-c">Prop hits</th></tr></thead>
          <tbody>{snaps.slice(0, 20).map((sn) => <tr key={sn.ID}><td className="mono nowrap">{new Date(sn.CreatedAt).toLocaleString()}</td><td className="fw5">{(sn.Top || []).join(', ')}</td><td className="mono ta-c">{sn.PropCount}</td></tr>)}</tbody>
        </table>
      </div>
    </div>
  )
}

function SettingsPage({ onUnauthorized }) {
  const [settings, setSettings] = useState(null)
  const [overrides, setOverrides] = useState({})
  const [props, setProps] = useState([])
  const [ticker, setTicker] = useState('')
  const [error, setError] = useState('')
  const [saved, setSaved] = useState('')

  const load = async () => {
    try {
      const [s, p] = await Promise.all([api.settings(), api.propList()])
      setSettings(s)
      setOverrides(s.overrides || {})
      setProps(p || [])
    } catch (err) {
      if (err.status === 401) onUnauthorized()
      else setError(err.message)
    }
  }

  useEffect(() => { load() }, [])

  const effective = settings?.effective
  const setOverride = (key, value) => setOverrides((old) => ({ ...old, [key]: String(value) }))

  const saveSettings = async () => {
    setError('')
    setSaved('')
    try {
      const res = await api.saveSettings(overrides)
      setSettings((old) => ({ ...old, effective: res.effective, overrides: res.overrides || {} }))
      setSaved('Saved')
    } catch (err) {
      if (err.status === 401) onUnauthorized()
      else setError(err.message)
    }
  }

  const resetSettings = async () => {
    const res = await api.saveSettings({})
    setOverrides({})
    setSettings((old) => ({ ...old, effective: res.effective, overrides: {} }))
  }

  const addProp = async () => {
    const inst = parseTicker(ticker)
    if (!inst) return
    await api.savePropList([...props, { Instrument: inst, Note: '' }])
    setTicker('')
    await load()
  }

  const removeProp = async (idx) => {
    await api.savePropList(props.filter((_, i) => i !== idx))
    await load()
  }

  return (
    <div className="page">
      {error && <div className="banner">{error}</div>}
      <div className="section">
        <div className="fx ac jb wrap gap12"><div className="sec-h">Prop list</div><div className="coverage">Items: <span className="fw7 pos">{props.length}</span></div></div>
        <div className="fx gap8 prop-add"><input className="input f1" placeholder="Add ticker" value={ticker} onChange={(e) => setTicker(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && addProp()} /><button className="btn" onClick={addProp}>Add</button></div>
        <div className="chips">{props.map((p, idx) => <div className="chip" key={`${(p.Instrument || p.instrument)?.base}-${idx}`}><span className="mono fw6">{displaySymbol(p.Instrument || p.instrument)}</span>{(p.Note || p.note) && <span className="chipnote">{p.Note || p.note}</span>}<button className="chipx" onClick={() => removeProp(idx)}>x</button></div>)}</div>
      </div>
      <div className="section">
        <div className="sec-h">Scoring</div>
        {effective ? <div className="settings-grid">
          <NumberField label="Min turnover $" value={overrides.min_volume_usd ?? effective.min_volume_usd} onChange={(v) => setOverride('min_volume_usd', v)} />
          <NumberField label="Min movement %" value={overrides.min_change_pct ?? effective.min_change_pct} onChange={(v) => setOverride('min_change_pct', v)} />
          <NumberField label="Correlation threshold" step="0.01" value={overrides.corr_threshold ?? effective.corr_threshold} onChange={(v) => setOverride('corr_threshold', v)} />
          <NumberField label="Top size" value={overrides.top_n ?? effective.top_n} onChange={(v) => setOverride('top_n', v)} />
          <SliderField label="Volatility" value={overrides['weights.volatility'] ?? effective.weights.volatility} onChange={(v) => setOverride('weights.volatility', v)} />
          <SliderField label="Turnover" value={overrides['weights.turnover'] ?? effective.weights.turnover} onChange={(v) => setOverride('weights.turnover', v)} />
          <SliderField label="Inplay" value={overrides['weights.inplay'] ?? effective.weights.inplay} onChange={(v) => setOverride('weights.inplay', v)} />
          <SliderField label="Corr penalty" value={overrides['weights.corr'] ?? effective.weights.corr} onChange={(v) => setOverride('weights.corr', v)} />
        </div> : <div className="skel wide" />}
        <div className="fx ac gap8 wrap settings-actions"><button className="btn btn-primary" onClick={saveSettings}>Save</button><button className="btn" onClick={resetSettings}>Reset defaults</button>{saved && <span className="pos fs13">{saved}</span>}<span className="dim fs13">changes apply on the next snapshot</span></div>
      </div>
    </div>
  )
}

function SummaryCard({ label, value, tone }) {
  return <div className="card"><div className="card-h">{label}</div><div className={`card-n ${tone || ''}`}>{value}</div></div>
}

function NumberField({ label, value, onChange, step = '1' }) {
  return <label className="field inline"><span>{label}</span><input className="input mono" type="number" step={step} value={value ?? ''} onChange={(e) => onChange(e.target.value)} /></label>
}

function SliderField({ label, value, onChange }) {
  return <div className="sldrow fx ac gap12"><span className="sldlabel">{label}</span><input className="f1" type="range" min="0" max="1" step="0.05" value={value ?? 0} onChange={(e) => onChange(e.target.value)} /><span className="mono sldval">{value}</span></div>
}

function ThemeButton({ theme, setTheme, light = false }) {
  const dark = document.documentElement.classList.contains('dark')
  return <button className={`themebtn ${light ? 'light' : ''}`} onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')} title="Theme">{dark ? <Moon size={16} /> : <Sun size={16} />}</button>
}

function displaySymbol(instrument = {}) {
  if (!instrument) return '-'
  const base = instrument.base || instrument.Base || ''
  const multiplier = instrument.multiplier || instrument.Multiplier || 1
  if (!multiplier || multiplier === 1) return `${base}USDT`
  return `${multiplier}${base}USDT`
}

function parseTicker(raw) {
  let symbol = raw.trim().toUpperCase()
  if (!symbol) return null
  symbol = symbol.replace(/USDT$/, '')
  for (const multiplier of [1000000, 10000, 1000, 100, 10]) {
    const prefix = String(multiplier)
    if (symbol.startsWith(prefix) && symbol.length > prefix.length) return { Base: symbol.slice(prefix.length), Multiplier: multiplier }
  }
  return { Base: symbol, Multiplier: 1 }
}

function fmtMoney(value = 0) {
  if (value >= 1_000_000_000) return `$${(value / 1_000_000_000).toFixed(2)}B`
  if (value >= 1_000_000) return `$${(value / 1_000_000).toFixed(1)}M`
  return `$${Math.round(value).toLocaleString()}`
}

function fmtPct(value = 0) {
  return `${Number(value || 0).toFixed(2)}%`
}

function fmtPrice(value = 0) {
  if (value >= 100) return Number(value).toFixed(2)
  if (value >= 1) return Number(value).toFixed(4)
  return Number(value).toPrecision(4)
}

createRoot(document.getElementById('root')).render(<App />)
