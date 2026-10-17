import React, { useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { ColorType, CrosshairMode, LineSeries, createChart } from 'lightweight-charts'
import ChartsPage from './ChartsPage'
import {
  addInstrumentChart,
  chartGridHasInstrument,
  readChartGrid,
  removeInstrumentCharts,
  subscribeChartGrid,
} from './chartGridStorage'
import {
  ArrowLeftRight,
  ArrowRight,
  BarChart3,
  Home,
  LogIn,
  LogOut,
  ListFilter,
  Moon,
  PanelTopClose,
  PanelTopOpen,
  RefreshCw,
  Search,
  Sun,
  Upload,
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
  register: (email, password) => api.request('/api/auth/register', { method: 'POST', body: JSON.stringify({ email, password }) }),
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
  dashboard: () => api.request('/api/dashboard/summary'),
  arbitrage: (limit = 30) => api.request(`/api/arbitrage/opportunities?limit=${limit}`),
  arbitrageStats: (days = 7) => api.request(`/api/arbitrage/stats?days=${days}`),
  refreshArbitrage: () => api.request('/api/arbitrage/refresh', { method: 'POST' }),
  arbitrageFees: () => api.request('/api/arbitrage/fees'),
  arbitrageChartInstruments: () => api.request('/api/arbitrage/charts/instruments'),
  propScreen: () => api.request('/api/prop-screen'),
  refreshPropScreen: () => api.request('/api/prop-screen/refresh', { method: 'POST' }),
}

function App() {
  const [path, setPath] = useState(() => `${window.location.pathname}${window.location.search}`)
  const [user, setUser] = useState(null)
  const [checked, setChecked] = useState(false)
  const [theme, setTheme] = useState(() => localStorage.getItem('theme') || 'system')
  const pathname = new URL(path, window.location.origin).pathname

  useEffect(() => {
    const onPop = () => setPath(`${window.location.pathname}${window.location.search}`)
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
  if (pathname === '/') return <Landing user={user} nav={nav} theme={theme} setTheme={setTheme} />
  if (pathname === '/auth') return <AuthPage user={user} setUser={setUser} nav={nav} theme={theme} setTheme={setTheme} />
  if (pathname.startsWith('/app')) {
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
        <div className="brand light">Scalp Assist</div>
        <div className="fx ac gap8">
          <ThemeButton theme={theme} setTheme={setTheme} light />
          {user ? (
            <button className="btn light" onClick={() => nav('/app')}>Open app</button>
          ) : (
            <>
              <button className="btn light" onClick={() => nav('/auth')}><LogIn size={16} />Sign in</button>
              <button className="btn btn-primary" onClick={() => nav('/auth')}><UserPlus size={16} />Register</button>
            </>
          )}
        </div>
      </header>
      <section className="landing-copy">
        <div className="landing-kicker">Binance / Bybit / OKX</div>
        <h1>Scalp Assist</h1>
        <p>Private futures workspace with market screening and cross-exchange spread monitoring in one service.</p>
        <div className="landing-actions">
          <button className="btn btn-primary big" onClick={() => nav(user ? '/app' : '/auth')}>Open workspace</button>
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
    if (user) nav('/app')
  }, [user])

  const submit = async (event) => {
    event.preventDefault()
    setLoading(true)
    setError('')
    try {
      const me = mode === 'login'
        ? await api.login(email, password)
        : await api.register(email, password)
      setUser(me)
      nav('/app')
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="app auth-shell">
      <header className="auth-top">
        <button className="brand as-button" onClick={() => nav('/')}>Scalp Assist</button>
        <ThemeButton theme={theme} setTheme={setTheme} />
      </header>
      <form className="auth-panel" onSubmit={submit}>
        <div className="seg">
          <button type="button" className={mode === 'login' ? 'on' : ''} onClick={() => setMode('login')}>Sign in</button>
          <button type="button" className={mode === 'register' ? 'on' : ''} onClick={() => setMode('register')}>Register</button>
        </div>
        <label className="field">
          <span>Email</span>
          <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" maxLength={254} required />
        </label>
        <label className="field">
          <span>Password</span>
          <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete={mode === 'login' ? 'current-password' : 'new-password'} minLength={mode === 'register' ? 8 : undefined} required />
        </label>
        {mode === 'register' && <div className="dim">At least 8 characters. Email confirmation is not enabled.</div>}
        {error && <div className="banner compact">{error}</div>}
        <button className="btn btn-primary full" disabled={loading}>
          {loading && <span className="spinner" />} {mode === 'login' ? 'Sign in' : 'Create account'}
        </button>
      </form>
    </div>
  )
}

function PrivateApp({ path, nav, user, setUser, onUnauthorized, theme, setTheme }) {
  const route = new URL(path, window.location.origin)
  const pathname = route.pathname
  const chartInstrumentID = route.searchParams.get('instrument') || ''
  const contour = pathname === '/app'
    ? 'dashboard'
    : pathname.startsWith('/app/charts')
      ? 'charts'
    : pathname.startsWith('/app/arbitrage')
      ? 'arbitrage'
      : pathname.startsWith('/app/prop-screen')
        ? 'prop-screen'
        : 'screener'
  const screenerView = pathname.endsWith('/history') ? 'history' : pathname.endsWith('/settings') ? 'settings' : pathname.endsWith('/wiki') ? 'wiki' : 'top'
  const propScreenView = pathname.endsWith('/wiki') ? 'wiki' : 'list'
  const arbitrageView = pathname.endsWith('/charts') ? 'charts' : pathname.endsWith('/info') ? 'info' : pathname.endsWith('/settings') ? 'settings' : pathname.endsWith('/wiki') ? 'wiki' : 'list'
  const logout = async () => {
    try { await api.logout() } catch {}
    setUser(null)
    nav('/')
  }
  return (
    <div className="app">
      <div className="topbar">
        <div className="fx ac gap24">
          <button className="brand as-button" onClick={() => nav('/app')}>Scalp Assist</button>
          <div className="fx gap4 mainnav">
            <button className={`tab ${contour === 'dashboard' ? 'on' : ''}`} onClick={() => nav('/app')}><Home size={15} />Overview</button>
            <button className={`tab ${contour === 'screener' ? 'on' : ''}`} onClick={() => nav('/app/screener')}><BarChart3 size={15} />Screener</button>
            <button className={`tab ${contour === 'prop-screen' ? 'on' : ''}`} onClick={() => nav('/app/prop-screen')}><ListFilter size={15} />Prop screen</button>
            <button className={`tab ${contour === 'charts' ? 'on' : ''}`} onClick={() => nav('/app/charts')}><BarChart3 size={15} />Charts</button>
            <button className={`tab ${contour === 'arbitrage' ? 'on' : ''}`} onClick={() => nav('/app/arbitrage')}><ArrowLeftRight size={15} />Arbitrage</button>
          </div>
        </div>
        <div className="fx ac gap12">
          <span className="updated mono">{user.email}</span>
          <ThemeButton theme={theme} setTheme={setTheme} />
          <button className="themebtn" onClick={logout} title="Logout"><LogOut size={16} /></button>
        </div>
      </div>
      {contour === 'dashboard' && <DashboardPage nav={nav} onUnauthorized={onUnauthorized} />}
      {contour === 'screener' && <>
        <ScreenerNav current={screenerView} nav={nav} />
        {screenerView === 'top' && <TopPage onUnauthorized={onUnauthorized} />}
        {screenerView === 'history' && <HistoryPage onUnauthorized={onUnauthorized} />}
        {screenerView === 'settings' && <SettingsPage onUnauthorized={onUnauthorized} />}
        {screenerView === 'wiki' && <ScreenerWikiPage />}
      </>}
      {contour === 'prop-screen' && <>
        <PropScreenNav current={propScreenView} nav={nav} />
        {propScreenView === 'list' && <PropScreenPage onUnauthorized={onUnauthorized} />}
        {propScreenView === 'wiki' && <PropScreenWikiPage />}
      </>}
      {contour === 'charts' && <ChartsPage onUnauthorized={onUnauthorized} theme={theme} />}
      {contour === 'arbitrage' && <>
        <ArbitrageNav current={arbitrageView} nav={nav} />
        {arbitrageView === 'list' && <ArbitragePage onUnauthorized={onUnauthorized} nav={nav} />}
        {arbitrageView === 'charts' && <ArbitrageChartsPage onUnauthorized={onUnauthorized} theme={theme} nav={nav} initialInstrumentID={chartInstrumentID} />}
        {arbitrageView === 'info' && <ArbitrageInfoPage onUnauthorized={onUnauthorized} />}
        {arbitrageView === 'settings' && <div className="page empty-tab-page" role="tabpanel" aria-label="Arbitrage settings" />}
        {arbitrageView === 'wiki' && <ArbitrageWikiPage />}
      </>}
    </div>
  )
}

function ScreenerNav({ current, nav }) {
  return <ContourTabs
    label="Screener sections"
    current={current}
    nav={nav}
    tabs={[
    { id: 'top', label: 'Top', path: '/app/screener' },
    { id: 'history', label: 'History', path: '/app/screener/history' },
    { id: 'settings', label: 'Settings', path: '/app/screener/settings' },
    { id: 'wiki', label: 'Wiki', path: '/app/screener/wiki' },
    ]}
  />
}

function ArbitrageNav({ current, nav }) {
  return <ContourTabs
    label="Arbitrage sections"
    current={current}
    nav={nav}
    tabs={[
      { id: 'list', label: 'List', path: '/app/arbitrage' },
      { id: 'charts', label: 'Charts', path: '/app/arbitrage/charts' },
      { id: 'info', label: 'Info', path: '/app/arbitrage/info' },
      { id: 'settings', label: 'Settings', path: '/app/arbitrage/settings' },
      { id: 'wiki', label: 'Wiki', path: '/app/arbitrage/wiki' },
    ]}
  />
}

function PropScreenNav({ current, nav }) {
  return <ContourTabs
    label="Prop screen sections"
    current={current}
    nav={nav}
    tabs={[
      { id: 'list', label: 'List', path: '/app/prop-screen' },
      { id: 'wiki', label: 'Wiki', path: '/app/prop-screen/wiki' },
    ]}
  />
}

function ArbitrageInfoPage({ onUnauthorized }) {
  const [fees, setFees] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api.arbitrageFees()
      .then(setFees)
      .catch((err) => {
        if (err.status === 401) onUnauthorized()
        else setError(err.message)
      })
  }, [])

  const rates = fees?.rates || []
  return (
    <div className="page fee-info-page" role="tabpanel" aria-label="Arbitrage fee information">
      <div className="page-heading">
        <div><div className="page-kicker mono">Configured calculation inputs</div><h1>Exchange fees</h1></div>
        <span className="updated mono">1 bps = 0.01%</span>
      </div>
      <p className="fee-info-lead">
        These rates are taken from the running application configuration and are used in the Net T/T and Net M/T columns.
      </p>
      {error && <div className="banner">{error}</div>}
      <div className="tblwrap">
        <table className="tbl fee-table">
          <thead>
            <tr>
              <th>Exchange</th>
              <th className="ta-r">Maker</th>
              <th className="ta-r">Taker</th>
            </tr>
          </thead>
          <tbody>
            {!fees && !error && Array.from({ length: 3 }).map((_, index) => <tr key={index}><td colSpan="3"><div className="skel" /></td></tr>)}
            {rates.map((rate) => (
              <tr key={rate.exchange}>
                <td className="fw7">{exchangeFullLabel(rate.exchange)}</td>
                <td className="mono ta-r">{fmtFeeRate(rate.maker_bps)}</td>
                <td className="mono ta-r">{fmtFeeRate(rate.taker_bps)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="fee-info-note">
        <div><span className="mono">Net T/T</span>: taker fee of the buy exchange + taker fee of the sell exchange.</div>
        <div><span className="mono">Net M/T</span>: maker fee of the buy exchange + taker fee of the sell exchange.</div>
        Actual account fees can differ because of VIP tier, region, rebates, promotions, or fee-token discounts.
        Update <span className="mono">fees_bps</span> and <span className="mono">maker_fees_bps</span> in the service configuration to match the trading account.
      </div>
    </div>
  )
}

const chartExchanges = [
  ['binance', 'Binance', '#ef4444'],
  ['bybit', 'Bybit', '#f59e0b'],
  ['okx', 'OKX', '#3b82f6'],
]
const chartGrossProxyColor = '#8b5cf6'
const chartStoredGrossColor = '#10b981'

function ArbitrageChartsPage({ onUnauthorized, theme, nav, initialInstrumentID }) {
  const containerRef = useRef(null)
  const grossProxySeriesRef = useRef(null)
  const [instruments, setInstruments] = useState([])
  const [instrumentID, setInstrumentID] = useState('')
  const [interval, setInterval] = useState('1m')
  const [showGrossProxy, setShowGrossProxy] = useState(true)
  const [loadingCatalog, setLoadingCatalog] = useState(true)
  const [error, setError] = useState('')
  const [statuses, setStatuses] = useState(() => chartStatusMap('idle'))
  const [lastValues, setLastValues] = useState({})
  const [lastGrossProxy, setLastGrossProxy] = useState(null)
  const [lastStoredGross, setLastStoredGross] = useState(null)

  useEffect(() => {
    let cancelled = false
    let retry
    const loadCatalog = async () => {
      try {
        const items = await api.arbitrageChartInstruments()
        if (cancelled) return
        setInstruments(items || [])
        setInstrumentID((current) => current || preferredChartInstrument(items || []))
        setError('')
        if (!items?.length) retry = window.setTimeout(loadCatalog, 2500)
      } catch (err) {
        if (cancelled) return
        if (err.status === 401) onUnauthorized()
        else setError(err.message)
      } finally {
        if (!cancelled) setLoadingCatalog(false)
      }
    }
    loadCatalog()
    return () => {
      cancelled = true
      window.clearTimeout(retry)
    }
  }, [])

  useEffect(() => {
    if (initialInstrumentID && instruments.some((item) => item.id === initialInstrumentID)) {
      setInstrumentID(initialInstrumentID)
    }
  }, [initialInstrumentID, instruments])

  useEffect(() => {
    grossProxySeriesRef.current?.applyOptions({ visible: showGrossProxy })
  }, [showGrossProxy])

  useEffect(() => {
    if (!containerRef.current || !instrumentID) return undefined

    const style = getComputedStyle(document.documentElement)
    const background = style.getPropertyValue('--panel').trim() || '#ffffff'
    const text = style.getPropertyValue('--muted').trim() || '#68746e'
    const grid = style.getPropertyValue('--line').trim() || '#d9e0dc'
    const chartHeight = window.innerWidth <= 720 ? 500 : 600
    const chart = createChart(containerRef.current, {
      width: containerRef.current.clientWidth,
      height: chartHeight,
      layout: {
        background: { type: ColorType.Solid, color: background },
        textColor: text,
        fontFamily: '"IBM Plex Mono", ui-monospace, monospace',
        attributionLogo: true,
        panes: {
          separatorColor: grid,
          separatorHoverColor: text,
          enableResize: true,
        },
      },
      grid: {
        vertLines: { color: grid },
        horzLines: { color: grid },
      },
      crosshair: { mode: CrosshairMode.Normal },
      rightPriceScale: { borderColor: grid },
      timeScale: {
        borderColor: grid,
        timeVisible: true,
        secondsVisible: false,
      },
      localization: { priceFormatter: (value) => fmtChartPrice(value) },
    })
    const series = {}
    chartExchanges.forEach(([exchange, label, color]) => {
      series[exchange] = chart.addSeries(LineSeries, {
        title: label,
        color,
        lineWidth: 2,
        priceLineVisible: false,
        lastValueVisible: true,
      })
    })
    const storedGrossSeries = chart.addSeries(LineSeries, {
      title: 'Gross (saved bid/ask)',
      color: chartStoredGrossColor,
      lineWidth: 2,
      priceLineVisible: false,
      lastValueVisible: true,
      priceFormat: {
        type: 'custom',
        minMove: 0.1,
        formatter: (value) => `${Number(value).toFixed(1)} bps`,
      },
    }, 1)
    const grossProxySeries = chart.addSeries(LineSeries, {
      title: 'Gross proxy',
      color: chartGrossProxyColor,
      lineWidth: 2,
      lineStyle: 2,
      priceLineVisible: false,
      lastValueVisible: true,
      visible: showGrossProxy,
      priceFormat: {
        type: 'custom',
        minMove: 0.1,
        formatter: (value) => `${Number(value).toFixed(1)} bps`,
      },
    }, 1)
    grossProxySeriesRef.current = grossProxySeries
    storedGrossSeries.createPriceLine({
      price: 0,
      color: grid,
      lineWidth: 1,
      lineStyle: 2,
      axisLabelVisible: true,
      title: '0',
    })
    const panes = chart.panes()
    if (panes[0] && panes[1]) {
      panes[0].setHeight(chartHeight - 180)
      panes[1].setHeight(150)
    }

    const observer = new ResizeObserver((entries) => {
      const width = entries[0]?.contentRect?.width
      if (width) chart.applyOptions({ width })
    })
    observer.observe(containerRef.current)

    let active = true
    const closeValuesByTime = new Map()
    const storedGrossByTime = new Map()
    setStatuses(chartStatusMap('connecting'))
    setLastValues({})
    setLastGrossProxy(null)
    setLastStoredGross(null)
    setError('')
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const query = new URLSearchParams({ instrument: instrumentID, interval })
    const socket = new WebSocket(`${protocol}//${window.location.host}/api/arbitrage/charts/stream?${query}`)
    socket.onmessage = (message) => {
      if (!active) return
      try {
        const payload = JSON.parse(message.data)
        if (payload.type === 'snapshot') {
          const values = {}
          chartExchanges.forEach(([exchange]) => {
            const points = (payload.series?.[exchange] || []).map(chartValuePoint)
            series[exchange].setData(points)
            points.forEach((point) => storeChartClose(closeValuesByTime, exchange, point))
            if (points.length) values[exchange] = points[points.length - 1].value
          })
          const grossProxyPoints = chartGrossProxyPoints(closeValuesByTime)
          grossProxySeries.setData(grossProxyPoints)
          setLastGrossProxy(grossProxyPoints[grossProxyPoints.length - 1]?.value ?? null)
          const storedGrossPoints = (payload.stored_gross || []).map(chartValuePoint)
          storedGrossPoints.forEach((point) => storedGrossByTime.set(point.time, point.value))
          storedGrossSeries.setData(storedGrossPoints)
          setLastStoredGross(storedGrossPoints[storedGrossPoints.length - 1]?.value ?? null)
          setLastValues(values)
          if (payload.errors) {
            setStatuses((current) => {
              const next = { ...current }
              Object.keys(payload.errors).forEach((exchange) => { next[exchange] = 'degraded' })
              return next
            })
          }
          chart.timeScale().fitContent()
        } else if (payload.type === 'bar' && series[payload.exchange] && payload.point) {
          const point = chartValuePoint(payload.point)
          series[payload.exchange].update(point)
          const grossProxyPoint = storeChartClose(closeValuesByTime, payload.exchange, point)
          if (grossProxyPoint) {
            grossProxySeries.update(grossProxyPoint)
            setLastGrossProxy(grossProxyPoint.value)
          }
          setLastValues((current) => ({ ...current, [payload.exchange]: point.value }))
        } else if (payload.type === 'gross' && payload.point) {
          const point = chartValuePoint(payload.point)
          const current = storedGrossByTime.get(point.time)
          if (current == null || point.value > current) {
            storedGrossByTime.set(point.time, point.value)
            storedGrossSeries.update(point)
            setLastStoredGross(point.value)
          }
        } else if (payload.type === 'status' && payload.exchange) {
          setStatuses((current) => ({ ...current, [payload.exchange]: payload.status }))
        } else if (payload.type === 'error') {
          setError(payload.error || 'Chart stream error')
        }
      } catch {
        setError('Invalid chart stream response')
      }
    }
    socket.onerror = () => {
      if (active) setError('Chart WebSocket connection failed')
    }
    socket.onclose = () => {
      if (active) setStatuses(chartStatusMap('disconnected'))
    }

    return () => {
      active = false
      socket.close()
      observer.disconnect()
      if (grossProxySeriesRef.current === grossProxySeries) grossProxySeriesRef.current = null
      chart.remove()
    }
  }, [instrumentID, interval, theme])

  const selected = instruments.find((item) => item.id === instrumentID)
  return (
    <div className="page chart-page" role="tabpanel" aria-label="Arbitrage charts">
      <div className="page-heading">
        <div><div className="page-kicker mono">Cross-exchange prices</div><h1>Charts</h1></div>
        <span className="updated mono">{interval} close price</span>
      </div>
      <div className="chart-controls">
        <label className="chart-control">
          <span>Common pair</span>
          <select
            className="select chart-pair-select"
            value={instrumentID}
            disabled={loadingCatalog || !instruments.length}
            onChange={(event) => {
              const nextInstrumentID = event.target.value
              setInstrumentID(nextInstrumentID)
              nav(`/app/arbitrage/charts?instrument=${encodeURIComponent(nextInstrumentID)}`)
            }}
          >
            {!instruments.length && <option value="">{loadingCatalog ? 'Loading pairs…' : 'No common pairs'}</option>}
            {instruments.map((item) => <option value={item.id} key={item.id}>{displayArbitrageSymbol(item.instrument)}</option>)}
          </select>
        </label>
        <div className="chart-control">
          <span>Period</span>
          <div className="chart-period" role="group" aria-label="Chart period">
            {['1m', '5m'].map((value) => <button className={interval === value ? 'on' : ''} type="button" onClick={() => setInterval(value)} key={value}>{value}</button>)}
          </div>
        </div>
        <label className="chk fx ac gap6 chart-gross-toggle">
          <input type="checkbox" checked={showGrossProxy} onChange={(event) => setShowGrossProxy(event.target.checked)} />
          Approximate Gross
        </label>
      </div>
      {error && <div className="banner">{error}</div>}
      {!loadingCatalog && !instruments.length && !error && <div className="banner">The common three-exchange catalog is warming up. This page will retry automatically.</div>}
      <section className="chart-panel">
        <div className="chart-legend">
          {chartExchanges.map(([exchange, label, color]) => (
            <div className="chart-legend-item" key={exchange}>
              <span className="chart-series-dot" style={{ background: color }} />
              <span className="fw6">{label}</span>
              <span className="mono">{lastValues[exchange] == null ? '—' : fmtChartPrice(lastValues[exchange])}</span>
              <span className={`chart-stream-status ${statuses[exchange] || 'idle'}`}>{statuses[exchange] || 'idle'}</span>
            </div>
          ))}
          <div className="chart-legend-item">
            <span className="chart-series-dot" style={{ background: chartStoredGrossColor }} />
            <span className="fw6">Saved Gross</span>
            <span className="mono">{lastStoredGross == null ? '—' : `${lastStoredGross.toFixed(1)} bps`}</span>
            <span className="chart-stream-status saved">bid/ask</span>
          </div>
          {showGrossProxy && <div className="chart-legend-item chart-gross-proxy">
            <span className="chart-series-dot" style={{ background: chartGrossProxyColor }} />
            <span className="fw6">Gross proxy</span>
            <span className="mono">{lastGrossProxy == null ? '—' : `${lastGrossProxy.toFixed(1)} bps`}</span>
            <span className="chart-stream-status proxy">close</span>
          </div>}
        </div>
        <div className="chart-canvas" ref={containerRef} />
        {selected && <div className="chart-symbols mono">{chartExchanges.map(([exchange, label]) => `${label}: ${selected.symbols?.[exchange] || '—'}`).join(' · ')}</div>}
      </section>
    </div>
  )
}

function chartStatusMap(status) {
  return Object.fromEntries(chartExchanges.map(([exchange]) => [exchange, status]))
}

function preferredChartInstrument(items) {
  return items.find((item) => item.instrument?.base === 'BTC' && item.instrument?.multiplier === 1)?.id || items[0]?.id || ''
}

function chartValuePoint(point) {
  return { time: Number(point.time), value: Number(point.value) }
}

function storeChartClose(valuesByTime, exchange, point) {
  if (!Number.isFinite(point.time) || !Number.isFinite(point.value) || point.value <= 0) return null
  const values = valuesByTime.get(point.time) || {}
  values[exchange] = point.value
  valuesByTime.set(point.time, values)
  return chartGrossProxyPoint(point.time, values)
}

function chartGrossProxyPoints(valuesByTime) {
  return [...valuesByTime.entries()]
    .sort(([left], [right]) => left - right)
    .map(([time, values]) => chartGrossProxyPoint(time, values))
    .filter(Boolean)
}

function chartGrossProxyPoint(time, values) {
  const prices = chartExchanges.map(([exchange]) => Number(values[exchange]))
  if (prices.some((price) => !Number.isFinite(price) || price <= 0)) return null
  return {
    time: Number(time),
    value: (Math.max(...prices) / Math.min(...prices) - 1) * 10000,
  }
}

function ContourTabs({ label, current, nav, tabs }) {
  return (
    <nav className="contour-tabs" role="tablist" aria-label={label}>
      {tabs.map((tab) => (
        <a
          key={tab.id}
          className={`contour-tab ${current === tab.id ? 'on' : ''}`}
          href={tab.path}
          role="tab"
          aria-selected={current === tab.id}
          onClick={(event) => {
            event.preventDefault()
            nav(tab.path)
          }}
        >
          {tab.label}
        </a>
      ))}
    </nav>
  )
}

function DashboardPage({ nav, onUnauthorized }) {
  const [summary, setSummary] = useState(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      setSummary(await api.dashboard())
    } catch (err) {
      if (err.status === 401) onUnauthorized()
      else setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  const screener = summary?.screener || {}
  const arb = summary?.arbitrage || {}
  const prop = summary?.prop_screen || {}
  const exchanges = Object.entries(arb.exchanges || {})
  const propExchanges = Object.entries(prop.exchanges || {})
  const scoreRows = screener.top_score || []
  const scoreMore = Math.max(0, Number(screener.rows || 0) - scoreRows.length)
  const positiveRows = arb.positive_opportunities || []
  const positiveMore = Math.max(0, Number(arb.positive_total || 0) - positiveRows.length)
  return (
    <div className="page">
      <div className="page-heading">
        <div><div className="page-kicker mono">Workspace</div><h1>Overview</h1></div>
        <div className="fx ac gap12">
          <span className="updated mono">{latestTime(screener.updated_at, prop.updated_at, arb.updated_at)}</span>
          <button className="btn" type="button" disabled={loading} onClick={load}>
            {loading && <span className="spinner" />}<RefreshCw size={15} />Refresh cards
          </button>
        </div>
      </div>
      {error && <div className="banner">{error}</div>}
      <div className="contour-grid">
        <section className="contour-panel">
          <div className="contour-head"><BarChart3 size={20} /><div><div className="sec-h">Screener</div><div className="dim fs13 mono">{formatUpdated(screener.updated_at)}</div></div></div>
          <div className="contour-value">{screener.rows ?? 0}</div>
          <div className="dim">ranked instruments</div>
          <div className="overview-arb-list">
            <div className="overview-arb-row overview-score-row overview-arb-header">
              <span>Ticker</span><span>Score</span>
            </div>
            {scoreRows.map((item) => (
              <div className="overview-arb-row overview-score-row" key={displaySymbol(item.instrument)}>
                <span className="mono fw6">{displaySymbol(item.instrument)}</span>
                <span className="mono fw7 score">{Number(item.score || 0).toFixed(4)}</span>
              </div>
            ))}
            {!scoreRows.length && <div className="overview-arb-empty">No ranked instruments.</div>}
            {scoreMore > 0 && <div className="overview-arb-more mono">+{scoreMore} more on Screener</div>}
          </div>
          <button className="btn contour-open" onClick={() => nav('/app/screener')}>Open screener <ArrowRight size={16} /></button>
        </section>
        <section className="contour-panel">
          <div className="contour-head"><ListFilter size={20} /><div><div className="sec-h">Prop screen</div><div className="dim fs13 mono">{formatUpdated(prop.updated_at)}</div></div></div>
          <div className="contour-value">{prop.listed ?? 0}</div>
          <div className="dim">of {prop.prop_total ?? 0} prop-list coins available</div>
          <div className="exchange-states">{propExchanges.map(([name, state]) => <span className={`exchange-state ${state.ok ? 'ok' : 'bad'}`} key={name}><span className="status-dot" />{exchangeLabel(name)}</span>)}</div>
          <div className="overview-arb-list">
            <div className="overview-arb-row overview-score-row overview-arb-header">
              <span>Metric source</span><span>Coins</span>
            </div>
            <div className="overview-arb-row overview-score-row">
              <span>Binance</span><span className="mono fw7">{prop.binance_sources ?? 0}</span>
            </div>
            <div className="overview-arb-row overview-score-row">
              <span>Bybit fallback</span><span className="mono fw7">{prop.bybit_fallbacks ?? 0}</span>
            </div>
          </div>
          <button className="btn contour-open" onClick={() => nav('/app/prop-screen')}>Open prop screen <ArrowRight size={16} /></button>
        </section>
        <section className="contour-panel">
          <div className="contour-head"><ArrowLeftRight size={20} /><div><div className="sec-h">Arbitrage</div><div className="dim fs13 mono">{formatUpdated(arb.updated_at)}</div></div></div>
          <div className="contour-value">{arb.opportunities ?? 0}</div>
          <div className="dim">{arb.instruments ?? 0} common instruments</div>
          <div className="exchange-states">{exchanges.map(([name, state]) => <span className={`exchange-state ${state.ok ? 'ok' : 'bad'}`} key={name}><span className="status-dot" />{exchangeLabel(name)}</span>)}</div>
          <div className="overview-arb-list">
            <div className="overview-arb-row overview-arb-header">
              <span>Ticker</span><span>Route</span><span>Net T/T</span><span>Active</span>
            </div>
            {positiveRows.map((item) => (
              <div className="overview-arb-row" key={`${displayArbitrageSymbol(item.instrument)}-${item.buy_exchange}-${item.sell_exchange}`}>
                <span className="mono fw6">{displayArbitrageSymbol(item.instrument)}</span>
                <span className="overview-arb-route mono">{exchangeLabel(item.buy_exchange)} <ArrowRight size={12} /> {exchangeLabel(item.sell_exchange)}</span>
                <span className="mono fw7 pos">{fmtBps(item.net_spread_bps)}</span>
                <span className="mono">{fmtDuration(item.first_seen_at, item.observed_at)}</span>
              </div>
            ))}
            {!positiveRows.length && <div className="overview-arb-empty">No positive Net T/T opportunities.</div>}
            {positiveMore > 0 && <div className="overview-arb-more mono">+{positiveMore} more positive on List</div>}
          </div>
          <button className="btn contour-open" onClick={() => nav('/app/arbitrage')}>Open arbitrage <ArrowRight size={16} /></button>
        </section>
      </div>
    </div>
  )
}

function PropScreenPage({ onUnauthorized }) {
  const [run, setRun] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState({ key: 'coin', direction: 'asc' })
  const [minChange, setMinChange] = useState('')
  const [minTurnover, setMinTurnover] = useState('')
  const [selectingCharts, setSelectingCharts] = useState(false)
  const [selectedCharts, setSelectedCharts] = useState(() => readChartGrid())

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      setRun(await api.propScreen())
    } catch (err) {
      if (err.status === 401) onUnauthorized()
      else setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])
  useEffect(() => subscribeChartGrid(setSelectedCharts), [])

  const refresh = async () => {
    setLoading(true)
    setError('')
    try {
      setRun(await api.refreshPropScreen())
    } catch (err) {
      if (err.status === 401) onUnauthorized()
      else setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  const changeSort = (key) => {
    setSort((current) => ({
      key,
      direction: current.key === key && current.direction === 'asc' ? 'desc' : 'asc',
    }))
  }
  const parsedMinChange = Number(minChange)
  const parsedMinTurnover = Number(minTurnover)
  const minChangeValue = minChange === '' || !Number.isFinite(parsedMinChange) ? null : parsedMinChange
  const minTurnoverValue = minTurnover === '' || !Number.isFinite(parsedMinTurnover) ? null : parsedMinTurnover
  const rows = [...(run?.rows || [])]
    .filter((row) => {
      const needle = search.trim().toUpperCase()
      const matchesSearch = !needle || row.instrument?.base?.includes(needle) || row.source_symbol?.includes(needle)
      const matchesChange = minChangeValue == null || Math.abs(Number(row.change_pct_24h || 0)) >= Math.abs(minChangeValue)
      const matchesTurnover = minTurnoverValue == null || Number(row.turnover_usd || 0) >= minTurnoverValue
      return matchesSearch && matchesChange && matchesTurnover
    })
    .sort((left, right) => {
      let comparison = 0
      if (sort.key === 'coin') comparison = String(left.instrument?.base || '').localeCompare(String(right.instrument?.base || ''))
      if (sort.key === 'exchange') comparison = String(left.source_exchange || '').localeCompare(String(right.source_exchange || ''))
      if (sort.key === 'change') comparison = Number(left.change_pct_24h || 0) - Number(right.change_pct_24h || 0)
      if (sort.key === 'corr') comparison = Number(left.btc_corr_7d ?? -2) - Number(right.btc_corr_7d ?? -2)
      if (sort.key === 'turnover') comparison = Number(left.turnover_usd || 0) - Number(right.turnover_usd || 0)
      return sort.direction === 'asc' ? comparison : -comparison
    })
  const binanceSources = (run?.rows || []).filter((row) => row.source_exchange === 'binance').length
  const bybitFallbacks = (run?.rows || []).filter((row) => row.source_exchange === 'bybit').length
  const toggleChart = (instrument, checked) => {
    if (checked) {
      const result = addInstrumentChart(instrument)
      setSelectedCharts(result.charts)
      if (result.error) setError(result.error)
    } else {
      setSelectedCharts(removeInstrumentCharts(instrument))
    }
  }

  return (
    <div className="page">
      <div className="page-heading">
        <div><div className="page-kicker mono">Prop list / futures</div><h1>Prop screen</h1></div>
        <span className="updated mono">{formatUpdated(run?.observed_at)}</span>
      </div>
      <div className="fx ac gap12 wrap">
        <button className="btn btn-primary" type="button" disabled={loading} onClick={refresh}>
          {loading && <span className="spinner" />}<RefreshCw size={16} />Refresh now
        </button>
        <button className={`btn ${selectingCharts ? 'on' : ''}`} type="button" onClick={() => setSelectingCharts((value) => !value)}>
          <BarChart3 size={16} />{selectingCharts ? 'Done' : 'Set charts'}
        </button>
        <label className="searchbox"><Search size={15} /><input placeholder="Search coin" value={search} onChange={(event) => setSearch(event.target.value)} /></label>
        <div className="exchange-states inline-states">
          {Object.entries(run?.exchanges || {}).map(([name, state]) => (
            <span className={`exchange-state ${state.ok ? 'ok' : 'bad'}`} title={state.error || `${state.tickers} tickers`} key={name}>
              <span className="status-dot" />{exchangeFullLabel(name)}
            </span>
          ))}
        </div>
      </div>
      <div className="cards">
        <SummaryCard label="Prop list" value={run?.prop_total ?? 0} />
        <SummaryCard label="Listed" value={run?.listed ?? 0} />
        <SummaryCard label="Binance source" value={binanceSources} />
        <SummaryCard label="Bybit fallback" value={bybitFallbacks} />
      </div>
      <div className="prop-screen-filters">
        <label className="field inline">
          <span>Minimum absolute 24h change, %</span>
          <input
            className="input mono"
            type="number"
            min="0"
            step="0.1"
            placeholder="No minimum"
            value={minChange}
            onChange={(event) => setMinChange(event.target.value)}
          />
        </label>
        <label className="field inline">
          <span>Minimum 24h turnover, $</span>
          <input
            className="input mono"
            type="number"
            min="0"
            step="1000000"
            placeholder="No minimum"
            value={minTurnover}
            onChange={(event) => setMinTurnover(event.target.value)}
          />
        </label>
        <span className="updated mono prop-screen-visible">showing {rows.length} of {run?.listed ?? 0}</span>
      </div>
      {error && <div className="banner">{error}</div>}
      <div className="tblwrap">
        <table className="tbl prop-screen-table">
          <thead>
            <tr>
              {selectingCharts && <th className="chart-select-cell"><span className="sr-only">Chart</span></th>}
              <PropSortHeader label="Coin" sortKey="coin" sort={sort} onSort={changeSort} />
              <th>Available</th>
              <PropSortHeader label="Source" sortKey="exchange" sort={sort} onSort={changeSort} />
              <th>Contract</th>
              <PropSortHeader label="24h change" sortKey="change" sort={sort} onSort={changeSort} align="right" />
              <PropSortHeader label="BTC corr 7d" sortKey="corr" sort={sort} onSort={changeSort} align="right" />
              <PropSortHeader label="24h turnover" sortKey="turnover" sort={sort} onSort={changeSort} align="right" />
              <th className="ta-r">Ticker time</th>
            </tr>
          </thead>
          <tbody>
            {loading && !run
              ? Array.from({ length: 8 }).map((_, index) => <tr key={index}><td colSpan={selectingCharts ? 9 : 8}><div className="skel" /></td></tr>)
              : rows.map((row) => {
                const chartInstrument = {
                  base: row.instrument?.base,
                  multiplier: row.contract_multiplier || row.instrument?.multiplier || 1,
                }
                return (
                <tr key={instrumentKey(row.instrument)}>
                  {selectingCharts && (
                    <td className="chart-select-cell">
                      <input
                        type="checkbox"
                        aria-label={`Show ${row.instrument?.base || 'coin'} chart`}
                        checked={chartGridHasInstrument(selectedCharts, chartInstrument)}
                        onChange={(event) => toggleChart(chartInstrument, event.target.checked)}
                      />
                    </td>
                  )}
                  <td className="mono fw7">{row.instrument?.base || '-'}</td>
                  <td>
                    <span className="fx ac gap4">
                      {row.instrument?.on_binance && <span className="badge">BN</span>}
                      {row.instrument?.on_bybit && <span className="badge">BB</span>}
                    </span>
                  </td>
                  <td><span className="badge">{exchangeFullLabel(row.source_exchange)}</span></td>
                  <td className="mono">{row.source_symbol || '-'}</td>
                  <td className={`mono ta-r fw7 ${Number(row.change_pct_24h) >= 0 ? 'pos' : 'neg'}`}>
                    {formatSignedPct(row.change_pct_24h)}
                  </td>
                  <td className="mono ta-r">{formatCorrelation(row.btc_corr_7d)}</td>
                  <td className="mono ta-r">{fmtMoney(row.turnover_usd)}</td>
                  <td className="mono ta-r updated">{formatTickerTime(row.updated_at)}</td>
                </tr>
              )})}
            {!loading && rows.length === 0 && (
              <tr>
                <td colSpan={selectingCharts ? 9 : 8} className="empty-cell">
                  {(run?.rows || []).length ? 'No coins match the current filters.' : 'No prop-list coins are available on Binance or Bybit.'}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function PropSortHeader({ label, sortKey, sort, onSort, align }) {
  const active = sort.key === sortKey
  return (
    <th className={align === 'right' ? 'ta-r' : ''}>
      <button className={`sort-header ${active ? 'on' : ''}`} type="button" onClick={() => onSort(sortKey)}>
        {label}<span aria-hidden="true">{active ? (sort.direction === 'asc' ? '↑' : '↓') : '↕'}</span>
      </button>
    </th>
  )
}

function PropScreenWikiPage() {
  const columns = [
    ['Coin', 'Монета из общей prop list. На экране остаются только монеты с активным perpetual-контрактом на Binance или Bybit.'],
    ['Available', 'Биржи, на которых найден подходящий контракт: BN — Binance, BB — Bybit.'],
    ['Source', 'Биржа, из которой взяты изменение и оборот. Binance приоритетен; Bybit используется как fallback.'],
    ['Contract', 'Фактический биржевой символ. Например, запись PEPE может быть сопоставлена с контрактом 1000PEPEUSDT.'],
    ['24h change', 'Процентное изменение цены за последние 24 часа по данным биржи-источника.'],
    ['BTC corr 7d', 'Корреляция Пирсона часовых доходностей монеты и BTC за последние 7 дней. Диапазон от −1 до +1.'],
    ['24h turnover', 'Оборот контракта в quote currency за последние 24 часа, отображаемый в долларах.'],
    ['Ticker time', 'Время рыночного payload, из которого взяты 24h-метрики.'],
  ]
  return (
    <div className="page">
      <div className="page-heading">
        <div><div className="page-kicker mono">Prop screen documentation</div><h1>Wiki</h1></div>
      </div>
      <p className="wiki-lead">
        Prop screen помогает быстро найти ликвидные и активно движущиеся монеты из prop list, доступные на Binance или Bybit. Экран предназначен для отбора кандидатов на дальнейший ручной анализ, а не для автоматического торгового сигнала.
      </p>

      <section className="wiki-section">
        <h2>Рабочий процесс</h2>
        <ol className="wiki-steps">
          <li>Задайте минимальный модуль изменения. Порог 3% оставит как +5%, так и −5%, но скроет движения между −3% и +3%.</li>
          <li>Задайте минимальный оборот, чтобы убрать слишком малоликвидные контракты.</li>
          <li>Отсортируйте таблицу по изменению, обороту или корреляции.</li>
          <li>Сверьте фактический контракт и источник данных, особенно для тикеров с множителем 100 или 1000.</li>
          <li>Используйте корреляцию вместе с направлением движения и объёмом, затем переходите к графику и ручной оценке сетапа.</li>
        </ol>
      </section>

      <section className="wiki-section">
        <h2>Как читать корреляцию с BTC</h2>
        <dl className="wiki-terms">
          <dt>От +0,8 до +1</dt>
          <dd>Сильная положительная связь: монета обычно движется в ту же сторону, что и BTC. Движение может быть частью общего рыночного импульса.</dd>
          <dt>От +0,5 до +0,8</dt>
          <dd>Умеренная положительная связь. BTC остаётся важным фактором, но собственное движение монеты заметнее.</dd>
          <dt>От −0,5 до +0,5</dt>
          <dd>Слабая линейная связь на текущем окне. Такие монеты могут быть интересны для поиска более самостоятельного движения.</dd>
          <dt>От −1 до −0,5</dt>
          <dd>Выраженная обратная связь: доходности часто имеют противоположный знак. Это не гарантирует продолжения и не делает монету надёжным хеджем.</dd>
          <dt>n/a</dt>
          <dd>Недостаточно минимум 100 совместимых часовых доходностей, контракт слишком новый либо свечи временно не удалось получить.</dd>
        </dl>
        <div className="wiki-warning">
          Корреляция описывает недавнюю совместную динамику, а не предсказывает следующую свечу. Она может быстро измениться при новостях, листингах и смене рыночного режима.
        </div>
      </section>

      <section className="wiki-section">
        <h2>Практическое применение</h2>
        <ul className="wiki-steps">
          <li>Для поиска общего рыночного импульса смотрите сильное движение, достаточный оборот и высокую положительную корреляцию.</li>
          <li>Для поиска самостоятельного движения обращайте внимание на большой модуль изменения при низкой абсолютной корреляции.</li>
          <li>Высокая корреляция означает дополнительную зависимость позиции от BTC; контролируйте одновременную экспозицию по нескольким похожим монетам.</li>
          <li>Отрицательная корреляция не является самостоятельным основанием для long или short.</li>
        </ul>
      </section>

      <section className="wiki-section">
        <h2>Столбцы</h2>
        <dl className="wiki-terms">
          {columns.map(([term, description]) => <React.Fragment key={term}><dt>{term}</dt><dd>{description}</dd></React.Fragment>)}
        </dl>
      </section>

      <section className="wiki-section">
        <h2>Добавление на страницу Charts</h2>
        <p className="wiki-copy">
          Кнопка Set charts включает чекбоксы слева от монет. Отмеченная монета добавляется в общую сетку
          Charts с таймфреймом 1 час. Снятие отметки удаляет из сетки все графики этой монеты.
          Список хранится в браузере и общий для вкладок Screener, Prop screen и Charts.
        </p>
      </section>

      <section className="wiki-section">
        <h2>Обновление и ограничения</h2>
        <ul className="wiki-steps">
          <li>24h change и turnover обновляются раз в минуту или по кнопке Refresh now.</li>
          <li>Корреляция рассчитывается после запуска и затем в медленном цикле примерно раз в 2 часа.</li>
          <li>Приоритет Binance применяется только при наличии валидных данных; при деградации используется Bybit.</li>
          <li>Метрики относятся к фьючерсному контракту и могут отличаться от spot-рынка.</li>
        </ul>
      </section>
    </div>
  )
}

function ArbitragePage({ onUnauthorized, nav }) {
  const [run, setRun] = useState(null)
  const [stats, setStats] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [search, setSearch] = useState('')

  const load = async () => {
    setLoading(true)
    setError('')
    try {
      const [latest, history] = await Promise.all([api.arbitrage(30), api.arbitrageStats(7)])
      setRun(latest)
      setStats(history)
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
      await api.refreshArbitrage()
      await load()
    } catch (err) {
      if (err.status === 401) onUnauthorized()
      else setError(err.message)
      setLoading(false)
    }
  }

  const rows = (run?.opportunities || []).filter((item) => {
    const symbol = displayArbitrageSymbol(item.instrument)
    return !search || symbol.includes(search.toUpperCase())
  })
  const positive = (run?.opportunities || []).filter((item) => item.net_spread_bps > 0).length
  return (
    <div className="page">
      <div className="page-heading">
        <div><div className="page-kicker mono">Perpetual / USDT</div><h1>Arbitrage</h1></div>
        <span className="updated mono">{formatUpdated(run?.observed_at)}</span>
      </div>
      <div className="fx ac gap12 wrap">
        <button className="btn btn-primary" onClick={refresh}>{loading && <span className="spinner" />}<RefreshCw size={16} />Refresh now</button>
        <label className="searchbox"><Search size={15} /><input placeholder="Search ticker" value={search} onChange={(e) => setSearch(e.target.value)} /></label>
        <div className="exchange-states inline-states">{Object.entries(run?.exchanges || {}).map(([name, state]) => <span className={`exchange-state ${state.ok ? 'ok' : 'bad'}`} title={state.error || `${state.quotes} quotes`} key={name}><span className="status-dot" />{exchangeLabel(name)}</span>)}</div>
      </div>
      <div className="cards">
        <SummaryCard label="Common instruments" value={run?.instruments ?? 0} />
        <SummaryCard label="Visible spreads" value={run?.opportunities?.length ?? 0} />
        <SummaryCard label="Net positive" value={positive} tone="pos" />
        <SummaryCard label="7d max net" value={`${Number(stats?.max_net_bps || 0).toFixed(1)} bps`} />
      </div>
      {error && <div className="banner">{error}</div>}
      <div className="tblwrap">
        <table className="tbl arb-table">
          <thead><tr><th>Ticker</th><th>Route</th><th className="ta-r">Buy ask</th><th className="ta-r">Sell bid</th><th className="ta-r">Gross</th><th className="ta-r" title="Taker on both exchanges">Net T/T</th><th className="ta-r" title="Maker on the buy exchange, taker on the sell exchange">Net M/T</th><th className="ta-r">Top capacity</th><th className="ta-r">Funding / h</th><th className="ta-r">Active</th></tr></thead>
          <tbody>
            {loading && !run ? Array.from({ length: 8 }).map((_, i) => <tr key={i}><td colSpan="10"><div className="skel" /></td></tr>) : rows.map((item) => (
              <tr className="arb-list-row" key={`${displayArbitrageSymbol(item.instrument)}-${item.buy_exchange}-${item.sell_exchange}`}>
                <td className="fw6 mono">
                  <a
                    className="ticker-chart-link"
                    href={`/app/arbitrage/charts?instrument=${encodeURIComponent(arbitrageChartInstrumentID(item.instrument))}`}
                    onClick={(event) => {
                      event.preventDefault()
                      nav(event.currentTarget.getAttribute('href'))
                    }}
                  >
                    {displayArbitrageSymbol(item.instrument)}
                  </a>
                </td>
                <td><span className="arb-route"><span className="badge">{exchangeLabel(item.buy_exchange)}</span><ArrowRight size={14} /><span className="badge">{exchangeLabel(item.sell_exchange)}</span></span></td>
                <td className="mono ta-r">{fmtPrice(item.buy_ask)}</td>
                <td className="mono ta-r">{fmtPrice(item.sell_bid)}</td>
                <td className="mono ta-r">{fmtBps(item.gross_spread_bps)}</td>
                <td className={`mono ta-r fw7 ${item.net_spread_bps > 0 ? 'pos' : 'neg'}`}>{fmtBps(item.net_spread_bps)}</td>
                <td className={`mono ta-r fw7 ${item.net_maker_taker_bps > 0 ? 'pos' : 'neg'}`}>{fmtBps(item.net_maker_taker_bps)}</td>
                <td className="mono ta-r">{fmtMoney(item.capacity_usd)}</td>
                <td className="mono ta-r">{item.funding_edge_bps_hour == null ? 'n/a' : fmtBps(item.funding_edge_bps_hour)}</td>
                <td className="mono ta-r">{fmtDuration(item.first_seen_at, item.observed_at)}</td>
              </tr>
            ))}
            {!loading && rows.length === 0 && <tr><td colSpan="10" className="empty-cell">No spreads match the current thresholds.</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function ArbitrageWikiPage() {
  const columns = [
    ['Ticker', 'Нормализованный фьючерсный инструмент. Множитель является частью тикера: PEPE и 1000PEPE считаются разными контрактами.'],
    ['Route', 'Направление: первая биржа — купить по Ask, вторая — продать по Bid. Например, Binance → OKX означает long на Binance и short на OKX.'],
    ['Buy ask', 'Лучшая доступная цена продажи в стакане биржи покупки. По этой цене предполагается открыть long.'],
    ['Sell bid', 'Лучшая доступная цена покупки в стакане биржи продажи. По этой цене предполагается открыть short.'],
    ['Gross', 'Разница между Sell bid и Buy ask до комиссий, в базисных пунктах. 1 bps = 0,01%.'],
    ['Net T/T', 'Gross за вычетом предполагаемых taker-комиссий обеих бирж. По этому значению выполняются фильтрация и сортировка.'],
    ['Net M/T', 'Gross за вычетом maker-комиссии биржи покупки и taker-комиссии биржи продажи. Исполнение maker-ордера не гарантируется. Funding, проскальзывание и задержка исполнения сюда не входят.'],
    ['Top capacity', 'Максимальный долларовый объём, видимый одновременно на первом уровне Ask и Bid. Это не полная глубина стакана.'],
    ['Funding / h', 'Оценка разницы funding выбранного long/short-направления, приведённая к одному часу. Положительное значение благоприятно для маршрута, n/a означает недостаток данных.'],
    ['Active', 'Сколько времени это же направление непрерывно остаётся в текущем списке. После длительного исчезновения отсчёт начинается заново.'],
  ]
  return (
    <div className="page wiki-page" role="tabpanel" aria-label="Arbitrage wiki">
      <div className="page-heading">
        <div><div className="page-kicker mono">Справка</div><h1>Как читать арбитраж</h1></div>
      </div>
      <p className="wiki-lead">
        Страница List сравнивает бессрочные USDT-фьючерсы Binance, Bybit и OKX и показывает направления,
        где цена покупки на одной бирже ниже цены продажи на другой.
      </p>

      <section className="wiki-section">
        <h2>List и переход к графику</h2>
        <p>
          При наведении строка таблицы подсвечивается. Нажмите на тикер в первом столбце, чтобы открыть вкладку Charts
          сразу с выбранным инструментом. Выбранная пара записывается в адрес страницы, поэтому сохраняется после
          обновления и корректно восстанавливается при навигации назад или вперёд.
        </p>
      </section>

      <section className="wiki-section">
        <h2>Info</h2>
        <p>
          Вкладка Info показывает maker- и taker-комиссии из текущей конфигурации сервиса, а также суммы,
          используемые для сценариев Net M/T и Net T/T. Это расчётные настройки приложения, а не автоматически
          полученный тариф биржевого аккаунта: VIP-уровень, региональные условия, скидки и rebates нужно учитывать
          вручную в конфигурации.
        </p>
      </section>

      <section className="wiki-section">
        <h2>Charts</h2>
        <p>
          Вкладка Charts строит три линии цены закрытия свечи выбранного контракта: Binance, Bybit и OKX.
          В селектор попадают только USDT perpetual-контракты, одновременно найденные на всех трёх биржах.
          Доступны интервалы 1 и 5 минут.
        </p>
        <p>
          Binance показан красной линией, Bybit — оранжевой, OKX — синей. Точки всех бирж привязаны к началу
          соответствующей минутной свечи, чтобы линии сравнивались на общей временной шкале без визуального сдвига.
          Выбор другой пары в селекторе также обновляет адрес страницы.
        </p>
        <p>
          В нижней синхронной панели зелёная линия Saved Gross строится по сохранённым Buy ask и Sell bid:
          <span className="mono"> (Sell bid / Buy ask − 1) × 10 000</span>. Для каждой минуты показывается лучшее
          сохранённое направление. Линия может иметь разрывы, если инструмент не прошёл фильтры List или в этот
          период не было сохранённой возможности.
        </p>
        <p>
          Фиолетовая пунктирная линия Gross proxy рассчитывается по close-ценам:
          <span className="mono"> (max close / min close − 1) × 10 000</span>. Она строится только для свечей,
          доступных одновременно у всех трёх бирж, и не является исполнимым Gross по стакану. Чекбокс
          Approximate Gross над графиком включает или скрывает эту линию; Saved Gross отображается всегда.
        </p>
        <p>
          История загружается через публичные REST API, а открытая свеча обновляется через WebSocket.
          Эти линии показывают последнюю цену сделки внутри свечи, а не лучшие Bid/Ask из таблицы List,
          поэтому график нельзя использовать для самостоятельного пересчёта арбитражного Net.
        </p>
      </section>

      <section className="wiki-section">
        <h2>Порядок работы</h2>
        <ol className="wiki-steps">
          <li>Проверьте индикаторы бирж: для сравнения должны быть доступны минимум две.</li>
          <li>Найдите тикер через поиск и проверьте направление в колонке Route.</li>
          <li>Оцените Net после комиссий и убедитесь, что Top capacity подходит под размер позиции.</li>
          <li>Проверьте Funding / h и длительность Active, затем перепроверьте цены и стаканы непосредственно на биржах.</li>
          <li>Кнопка Refresh now запускает внеплановый REST-опрос; обычное обновление выполняется автоматически.</li>
        </ol>
      </section>

      <section className="wiki-section">
        <h2>Сводные показатели</h2>
        <dl className="wiki-terms">
          <dt>Common instruments</dt><dd>Количество нормализованных контрактов, найденных минимум на двух доступных биржах.</dd>
          <dt>Visible spreads</dt><dd>Число направлений, прошедших текущие фильтры spread, свежести и ликвидности.</dd>
          <dt>Net positive</dt><dd>Количество строк с положительным Net T/T после двух taker-комиссий.</dd>
          <dt>7d max net</dt><dd>Максимальный сохранённый Net T/T за последние семь дней.</dd>
        </dl>
      </section>

      <section className="wiki-section">
        <h2>Столбцы таблицы</h2>
        <dl className="wiki-terms">
          {columns.map(([term, description]) => (
            <React.Fragment key={term}>
              <dt>{term}</dt>
              <dd>{description}</dd>
            </React.Fragment>
          ))}
        </dl>
      </section>

      <section className="wiki-section">
        <h2>Формулы</h2>
        <div className="wiki-formula mono">Gross = (Sell bid / Buy ask - 1) × 10 000 bps</div>
        <div className="wiki-formula mono">Net T/T = Gross - taker покупки - taker продажи</div>
        <div className="wiki-formula mono">Net M/T = Gross - maker покупки - taker продажи</div>
        <div className="wiki-formula mono">Top capacity = min(Ask qty в base × Ask, Bid qty в base × Bid)</div>
      </section>

      <section className="wiki-section wiki-warning">
        <h2>Важно</h2>
        <p>
          Положительный Net — это индикатор, а не гарантия исполнимой прибыли. Maker-ордер может не исполниться,
          а данные поступают через REST,
          котировки двух бирж могут измениться между запросом и сделкой, а доступный объём может исчезнуть.
          Перед любым решением вручную учитывайте проскальзывание, реальные комиссии, funding, ограничения
          контрактов и риск неполного исполнения одной стороны.
        </p>
      </section>
    </div>
  )
}

function ScreenerWikiPage() {
  const columns = [
    ['#', 'Место инструмента в текущем рейтинге. Чем меньше номер, тем выше итоговый Score.'],
    ['Ticker', 'Нормализованный тикер. Множитель является частью инструмента: PEPEUSDT и 1000PEPEUSDT не объединяются.'],
    ['Exchanges', 'BN — инструмент есть на Binance, BB — на Bybit. Метка NEW означает, что инструмент доступен только на Bybit.'],
    ['Price', 'Текущая цена. При наличии инструмента на обеих биржах приоритет имеют данные Binance, иначе используется Bybit.'],
    ['Turnover', 'Оборот за 24 часа в долларах. Если инструмент есть на двух биржах, их обороты суммируются.'],
    ['Range', 'Внутридневной диапазон: (High 24h − Low 24h) / Low 24h. Чем выше значение, тем активнее движение цены.'],
    ['Corr', 'Корреляция часовых доходностей инструмента с BTC за последние 7 дней. Значение ближе к 1 означает сильное совместное движение; n/a — данных недостаточно.'],
    ['Score', 'Итоговый относительный балл по волатильности, обороту, inplay и корреляции. Это ранг внутри текущей выборки, а не вероятность успешной сделки.'],
    ['Prop', 'Метка yes означает, что инструмент присутствует в вашем prop list.'],
  ]
  const details = [
    ['change', 'Изменение цены за последние 24 часа.'],
    ['funding', 'Текущая ставка funding фьючерса. Знак и размер нужно оценивать с учётом направления планируемой позиции.'],
    ['tick', 'Размер минимального шага цены относительно текущей цены, в процентах.'],
    ['inplay', 'Инструмент движется заметно сильнее растущего рынка либо идёт против общего роста.'],
  ]
  return (
    <div className="page wiki-page" role="tabpanel" aria-label="Screener wiki">
      <div className="page-heading">
        <div><div className="page-kicker mono">Справка</div><h1>Как читать скринер</h1></div>
      </div>
      <p className="wiki-lead">
        Скринер анализирует бессрочные фьючерсы Binance и Bybit, отбрасывает инструменты,
        не прошедшие фильтры объёма и движения, затем ранжирует оставшиеся монеты для ручного просмотра графиков.
      </p>

      <section className="wiki-section">
        <h2>Порядок работы</h2>
        <ol className="wiki-steps">
          <li>Начните с верхних строк Top: они получили самый высокий Score в текущем снимке рынка.</li>
          <li>Проверьте Turnover и Range, затем Corr, Inplay и наличие инструмента на нужной бирже.</li>
          <li>Нажмите на строку, чтобы увидеть изменение за сутки, funding, относительный tick и флаг inplay.</li>
          <li>Используйте поиск, фильтр биржи и prop only, чтобы сузить уже рассчитанный Top.</li>
          <li>Откройте график выбранного инструмента и отдельно оцените уровни, формации и новости.</li>
        </ol>
      </section>

      <section className="wiki-section">
        <h2>Сводные показатели</h2>
        <dl className="wiki-terms">
          <dt>Rows</dt><dd>Количество инструментов в текущем сохранённом Top.</dd>
          <dt>Prop hits</dt><dd>Сколько строк Top входит в ваш prop list.</dd>
          <dt>Inplay</dt><dd>Количество инструментов, которые сейчас выделяются относительно движения всего рынка.</dd>
          <dt>Bybit only</dt><dd>Количество инструментов, найденных на Bybit и отсутствующих на Binance.</dd>
        </dl>
      </section>

      <section className="wiki-section">
        <h2>Столбцы таблицы</h2>
        <dl className="wiki-terms">
          {columns.map(([term, description]) => (
            <React.Fragment key={term}>
              <dt>{term}</dt>
              <dd>{description}</dd>
            </React.Fragment>
          ))}
        </dl>
      </section>

      <section className="wiki-section">
        <h2>Данные раскрытой строки</h2>
        <dl className="wiki-terms">
          {details.map(([term, description]) => (
            <React.Fragment key={term}>
              <dt>{term}</dt>
              <dd>{description}</dd>
            </React.Fragment>
          ))}
        </dl>
      </section>

      <section className="wiki-section">
        <h2>Как рассчитывается Score</h2>
        <p className="wiki-copy">
          Range и Turnover переводятся в перцентили текущей выборки. К ним добавляется бонус Inplay
          и вычитается штраф за корреляцию с BTC. Если инструмент находится в inplay, штраф корреляции
          не применяется. Веса задаются во вкладке Settings.
        </p>
        <div className="wiki-formula mono">Score = Volatility + Turnover + Inplay − Corr penalty</div>
      </section>

      <section className="wiki-section">
        <h2>History, Settings и prop list</h2>
        <dl className="wiki-terms">
          <dt>History</dt><dd>Показывает частоту попадания инструментов в Top, лидеров периода и сохранённые снимки.</dd>
          <dt>Settings</dt><dd>Управляет минимальным оборотом, движением, порогом корреляции, весами Score и размером Top. Изменения применяются со следующим снимком.</dd>
          <dt>Prop list</dt><dd>Личный список разрешённых инструментов проп-компании. Его можно редактировать вручную или импортировать из TXT.</dd>
        </dl>
      </section>

      <section className="wiki-section">
        <h2>Добавление на страницу Charts</h2>
        <p className="wiki-copy">
          Кнопка Set charts показывает чекбоксы перед тикерами. Отметка добавляет свечной график с таймфреймом
          1 час, снятие отметки удаляет все графики этого инструмента. Максимум можно открыть 10 графиков.
        </p>
      </section>

      <section className="wiki-section wiki-warning">
        <h2>Важно</h2>
        <p>
          Высокий Score не является торговым сигналом. Скринер сокращает список инструментов для анализа,
          но не учитывает конкретную формацию, уровни, стакан, новости и ваш риск-менеджмент.
          Рыночные данные обновляются периодически, поэтому перед решением проверяйте актуальный график на бирже.
        </p>
      </section>
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
  const [selectingCharts, setSelectingCharts] = useState(false)
  const [selectedCharts, setSelectedCharts] = useState(() => readChartGrid())

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
  useEffect(() => subscribeChartGrid(setSelectedCharts), [])

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
  const toggleChart = (instrument, checked) => {
    if (checked) {
      const result = addInstrumentChart(instrument)
      setSelectedCharts(result.charts)
      if (result.error) setError(result.error)
    } else {
      setSelectedCharts(removeInstrumentCharts(instrument))
    }
  }

  return (
    <div className="page">
      <div className="fx ac gap12 wrap">
        <button className="btn btn-primary" onClick={refresh}>{loading && <span className="spinner" />}<RefreshCw size={16} />Refresh now</button>
        <button className={`btn ${selectingCharts ? 'on' : ''}`} type="button" onClick={() => setSelectingCharts((value) => !value)}>
          <BarChart3 size={16} />{selectingCharts ? 'Done' : 'Set charts'}
        </button>
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
          <thead><tr><th>#</th>{selectingCharts && <th className="chart-select-cell"><span className="sr-only">Chart</span></th>}<th>Ticker</th><th className="hide-sm">Exchanges</th><th className="ta-r">Price</th><th className="ta-r">Turnover</th><th className="ta-r">Range</th><th className="ta-r hide-sm">Corr</th><th className="ta-r">Score</th><th className="ta-c">Prop</th></tr></thead>
          <tbody>
            {loading && rows.length === 0 ? Array.from({ length: 8 }).map((_, i) => <tr key={i}><td colSpan={selectingCharts ? 10 : 9}><div className="skel" /></td></tr>) : filtered.map((row) => {
              const symbol = displaySymbol(row.instrument)
              const open = expanded === symbol
              return (
                <React.Fragment key={symbol}>
                  <tr className="rowmain" onClick={() => setExpanded(open ? null : symbol)}>
                    <td className="mono dim">{row.rank}</td>
                    {selectingCharts && (
                      <td className="chart-select-cell" onClick={(event) => event.stopPropagation()}>
                        <input
                          type="checkbox"
                          aria-label={`Show ${symbol} chart`}
                          checked={chartGridHasInstrument(selectedCharts, row.instrument)}
                          onChange={(event) => toggleChart(row.instrument, event.target.checked)}
                        />
                      </td>
                    )}
                    <td className="fw6">{symbol}</td>
                    <td className="hide-sm"><span className="fx gap4">{row.instrument.on_binance && <span className="badge">BN</span>}{row.instrument.on_bybit && <span className="badge">BB</span>}{row.bybit_only && <span className="badge warn">NEW</span>}</span></td>
                    <td className="mono ta-r">{fmtPrice(row.price)}</td>
                    <td className="mono ta-r">{fmtMoney(row.turnover_usd)}</td>
                    <td className="mono ta-r">{fmtPct(row.day_range_pct)}</td>
                    <td className={`mono ta-r hide-sm ${row.high_corr ? 'neg' : ''}`}>{row.btc_corr_7d == null ? 'n/a' : row.btc_corr_7d.toFixed(2)}</td>
                    <td className="mono ta-r score">{Number(row.score || 0).toFixed(4)}</td>
                    <td className="ta-c">{row.in_prop_list ? <span className="badge-prop">yes</span> : <span className="dash">-</span>}</td>
                  </tr>
                  {open && <tr className="detailrow"><td colSpan={selectingCharts ? 10 : 9}><div className="detail"><div className="metrics fx gap24 wrap mono"><span><span className="dim">change</span> {fmtPct(row.change_pct_24h)}</span><span><span className="dim">funding</span> {fmtPct(row.funding_rate * 100)}</span><span><span className="dim">tick</span> {Number(row.relative_tick_pct || 0).toFixed(4)}%</span><span><span className="dim">inplay</span> {row.is_inplay ? 'yes' : 'no'}</span></div></div></td></tr>}
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
  const [importStatus, setImportStatus] = useState('')
  const [importing, setImporting] = useState(false)
  const [propOpen, setPropOpen] = useState(false)

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

  const importPropFile = async (event) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    setError('')
    setImportStatus('')
    const isTextFile = file.name.toLowerCase().endsWith('.txt') || file.type === 'text/plain'
    if (!isTextFile) {
      setImportStatus('Only .txt files are supported')
      return
    }
    setImporting(true)
    try {
      const parsed = parsePropListText(await file.text())
      const existing = new Set(props.map((p) => instrumentKey(p.Instrument || p.instrument)))
      const seenInFile = new Set()
      const additions = []
      let duplicates = 0

      for (const instrument of parsed.instruments) {
        const key = instrumentKey(instrument)
        if (existing.has(key) || seenInFile.has(key)) {
          duplicates += 1
          continue
        }
        seenInFile.add(key)
        additions.push({ Instrument: instrument, Note: '' })
      }

      if (additions.length === 0) {
        setImportStatus(`Added 0, duplicates ${duplicates}, skipped ${parsed.skipped}`)
        return
      }

      await api.savePropList([...props, ...additions])
      setImportStatus(`Added ${additions.length}, duplicates ${duplicates}, skipped ${parsed.skipped}`)
      await load()
    } catch (err) {
      if (err.status === 401) onUnauthorized()
      else setError(err.message)
    } finally {
      setImporting(false)
    }
  }

  const removeProp = async (idx) => {
    await api.savePropList(props.filter((_, i) => i !== idx))
    await load()
  }

  return (
    <div className="page">
      {error && <div className="banner">{error}</div>}
      <div className="section">
        <div className="fx ac jb wrap gap12">
          <div className="fx ac gap12">
            <div className="sec-h">Prop list</div>
            <div className="coverage">Items: <span className="fw7 pos">{props.length}</span></div>
          </div>
          <button className="themebtn prop-toggle" onClick={() => setPropOpen((open) => !open)} title={propOpen ? 'Collapse prop list' : 'Expand prop list'}>
            {propOpen ? <PanelTopClose size={16} /> : <PanelTopOpen size={16} />}
          </button>
        </div>
        {propOpen && <>
          <div className="fx gap8 prop-add">
            <input className="input f1" placeholder="Add ticker" value={ticker} onChange={(e) => setTicker(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && addProp()} />
            <button className="btn" onClick={addProp}>Add</button>
            <label className={`btn upload-btn ${importing ? 'disabled' : ''}`}>
              {importing ? <span className="spinner" /> : <Upload size={16} />}
              Import TXT
              <input type="file" accept=".txt,text/plain" onChange={importPropFile} disabled={importing} />
            </label>
          </div>
          {importStatus && <div className="import-status mono">{importStatus}</div>}
          <div className="chips">{props.map((p, idx) => <div className="chip" key={`${(p.Instrument || p.instrument)?.base}-${idx}`}><span className="mono fw6">{displaySymbol(p.Instrument || p.instrument)}</span>{(p.Note || p.note) && <span className="chipnote">{p.Note || p.note}</span>}<button className="chipx" onClick={() => removeProp(idx)}>x</button></div>)}</div>
        </>}
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
  if (!symbol || !/^[A-Z0-9]+(USDT)?$/.test(symbol)) return null
  symbol = symbol.replace(/USDT$/, '')
  if (!symbol || !/[A-Z]/.test(symbol)) return null
  for (const multiplier of [1000000, 10000, 1000, 100, 10]) {
    const prefix = String(multiplier)
    if (symbol.startsWith(prefix) && symbol.length > prefix.length) {
      const base = symbol.slice(prefix.length)
      if (!/[A-Z]/.test(base)) return null
      return { Base: base, Multiplier: multiplier }
    }
  }
  return { Base: symbol, Multiplier: 1 }
}

function parsePropListText(text) {
  const tokens = text.split(/[,\r\n]+/)
  const instruments = []
  let skipped = 0
  for (const token of tokens) {
    const trimmed = token.trim()
    if (!trimmed) continue
    const instrument = parseTicker(trimmed)
    if (!instrument) {
      skipped += 1
      continue
    }
    instruments.push(instrument)
  }
  return { instruments, skipped }
}

function instrumentKey(instrument = {}) {
  const base = instrument.base || instrument.Base || ''
  const multiplier = instrument.multiplier || instrument.Multiplier || 1
  return `${base.toUpperCase()}:${multiplier}`
}

function displayArbitrageSymbol(instrument = {}) {
  const base = instrument.base || ''
  const quote = instrument.quote || 'USDT'
  const multiplier = instrument.multiplier || 1
  return `${multiplier === 1 ? '' : multiplier}${base}${quote}`
}

function arbitrageChartInstrumentID(instrument = {}) {
  const base = instrument.base || ''
  const quote = instrument.quote || 'USDT'
  const multiplier = instrument.multiplier || 1
  const kind = instrument.kind || 'linear_perpetual'
  return `${base}:${quote}:${multiplier}:${kind}`
}

function exchangeLabel(exchange = '') {
  const labels = { binance: 'BN', bybit: 'BB', okx: 'OKX' }
  return labels[String(exchange).toLowerCase()] || String(exchange).toUpperCase()
}

function exchangeFullLabel(exchange = '') {
  const labels = { binance: 'Binance', bybit: 'Bybit', okx: 'OKX' }
  return labels[String(exchange).toLowerCase()] || String(exchange)
}

function formatUpdated(value) {
  if (!value || new Date(value).getFullYear() <= 1) return 'waiting for data'
  return new Date(value).toLocaleString()
}

function latestTime(...values) {
  const valid = values.filter(Boolean).map((value) => new Date(value)).filter((value) => !Number.isNaN(value.getTime()) && value.getFullYear() > 1)
  if (!valid.length) return ''
  return `updated ${new Date(Math.max(...valid.map((value) => value.getTime()))).toLocaleTimeString()}`
}

function fmtBps(value = 0) {
  return `${Number(value || 0).toFixed(1)} bps`
}

function fmtFeeRate(value = 0) {
  const bps = Number(value || 0)
  return `${bps.toFixed(2)} bps (${(bps / 100).toFixed(3)}%)`
}

function fmtDuration(from, to) {
  if (!from || !to) return '-'
  const seconds = Math.max(0, Math.floor((new Date(to) - new Date(from)) / 1000))
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
}

function fmtMoney(value = 0) {
  if (value >= 1_000_000_000) return `$${(value / 1_000_000_000).toFixed(2)}B`
  if (value >= 1_000_000) return `$${(value / 1_000_000).toFixed(1)}M`
  return `$${Math.round(value).toLocaleString()}`
}

function fmtPct(value = 0) {
  return `${Number(value || 0).toFixed(2)}%`
}

function formatSignedPct(value = 0) {
  const number = Number(value || 0)
  return `${number > 0 ? '+' : ''}${number.toFixed(2)}%`
}

function formatTickerTime(value) {
  if (!value) return 'n/a'
  const date = new Date(value)
  if (Number.isNaN(date.getTime()) || date.getFullYear() <= 1) return 'n/a'
  return date.toLocaleTimeString()
}

function formatCorrelation(value) {
  if (value == null || !Number.isFinite(Number(value))) return 'n/a'
  return Number(value).toFixed(2)
}

function fmtPrice(value = 0) {
  if (value >= 100) return Number(value).toFixed(2)
  if (value >= 1) return Number(value).toFixed(4)
  return Number(value).toPrecision(4)
}

function fmtChartPrice(value = 0) {
  const number = Number(value)
  if (!Number.isFinite(number)) return '—'
  if (number >= 1000) return number.toLocaleString(undefined, { maximumFractionDigits: 2 })
  if (number >= 1) return number.toLocaleString(undefined, { maximumFractionDigits: 5 })
  return number.toPrecision(6)
}

createRoot(document.getElementById('root')).render(<App />)
