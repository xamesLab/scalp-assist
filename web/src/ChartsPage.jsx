import React, { useEffect, useMemo, useRef, useState } from 'react'
import { CandlestickSeries, ColorType, CrosshairMode, createChart } from 'lightweight-charts'
import { Plus, RefreshCw, X } from 'lucide-react'
import {
  MAX_CHARTS,
  addInstrumentChart,
  chartInstrumentKey,
  readChartGrid,
  removeChartSlot,
  subscribeChartGrid,
  updateChartSlot,
} from './chartGridStorage'

export default function ChartsPage({ onUnauthorized, theme }) {
  const [layout, setLayout] = useState(() => readChartGrid())
  const [catalog, setCatalog] = useState([])
  const [slotData, setSlotData] = useState({})
  const [error, setError] = useState('')
  const [dirty, setDirty] = useState(false)
  const [connection, setConnection] = useState('connecting')

  const loadCatalog = async () => {
    try {
      const response = await fetch('/api/charts/instruments', { credentials: 'include' })
      if (response.status === 401) {
        onUnauthorized()
        return
      }
      if (!response.ok) {
        let message = `${response.status} ${response.statusText}`
        try {
          const body = await response.json()
          message = body?.error?.message || message
        } catch {}
        throw new Error(message)
      }
      const instruments = await response.json()
      setCatalog(instruments)
      if (!instruments.length) throw new Error('No chart instruments are available on Binance or Bybit.')
    } catch (err) {
      setError(err.message)
      setConnection('unavailable')
    }
  }

  useEffect(() => { loadCatalog() }, [])
  useEffect(() => subscribeChartGrid((_charts, source) => {
    if (source === 'storage') setDirty(true)
  }), [])

  const catalogByKey = useMemo(() => new Map(catalog.map((item) => [item.id, item])), [catalog])
  const subscriptionSignature = layout
    .map((slot) => `${slot.id}:${chartInstrumentKey(slot.instrument)}:${slot.interval}`)
    .join('|')

  useEffect(() => {
    if (!catalog.length) return undefined
    let active = true
    let socket
    let reconnectTimer
    let attempt = 0

    const connect = () => {
      if (!active) return
      setConnection(attempt ? 'reconnecting' : 'connecting')
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      socket = new WebSocket(`${protocol}//${window.location.host}/api/charts/stream`)
      socket.onopen = () => {
        if (!active) return
        attempt = 0
        setConnection('connected')
        for (const slot of layout) {
          const instrumentID = chartInstrumentKey(slot.instrument)
          if (!catalogByKey.has(instrumentID)) {
            setSlotData((current) => ({
              ...current,
              [slot.id]: { candles: [], status: 'unavailable', error: 'Instrument is not available on Binance or Bybit.' },
            }))
            continue
          }
          socket.send(JSON.stringify({
            type: 'subscribe',
            slot_id: slot.id,
            instrument_id: instrumentID,
            interval: slot.interval,
          }))
        }
      }
      socket.onmessage = (event) => {
        let message
        try { message = JSON.parse(event.data) } catch { return }
        if (!message.slot_id) return
        setSlotData((current) => {
          const previous = current[message.slot_id] || { candles: [], status: 'connecting', error: '' }
          if (message.type === 'snapshot') {
            return {
              ...current,
              [message.slot_id]: {
                ...previous,
                candles: message.candles || [],
                instrument: message.instrument,
                status: 'connected',
                error: '',
              },
            }
          }
          if (message.type === 'bar' && message.candle) {
            const candles = mergeCandle(previous.candles || [], message.candle)
            return { ...current, [message.slot_id]: { ...previous, candles, status: 'connected', error: '' } }
          }
          if (message.type === 'status') {
            return {
              ...current,
              [message.slot_id]: { ...previous, status: message.status || previous.status, error: message.error || '' },
            }
          }
          if (message.type === 'error') {
            return {
              ...current,
              [message.slot_id]: { ...previous, status: 'unavailable', error: message.error || 'Chart stream error.' },
            }
          }
          return current
        })
      }
      socket.onclose = () => {
        if (!active) return
        setConnection('reconnecting')
        attempt += 1
        reconnectTimer = window.setTimeout(connect, Math.min(30_000, 1000 * 2 ** Math.min(attempt - 1, 5)))
      }
      socket.onerror = () => socket.close()
    }

    connect()
    return () => {
      active = false
      window.clearTimeout(reconnectTimer)
      if (socket && socket.readyState < WebSocket.CLOSING) socket.close()
    }
  }, [catalog, subscriptionSignature])

  const refreshLayout = () => {
    setLayout(readChartGrid())
    setSlotData({})
    setDirty(false)
    loadCatalog()
  }
  const updateSlot = (id, patch) => {
    setLayout(updateChartSlot(id, patch))
    setSlotData((current) => {
      const next = { ...current }
      delete next[id]
      return next
    })
  }
  const addBTC = () => {
    const result = addInstrumentChart({ base: 'BTC', multiplier: 1 })
    setLayout(result.charts)
    setError(result.error)
  }
  const removeSlot = (id) => {
    setLayout(removeChartSlot(id))
    setSlotData((current) => {
      const next = { ...current }
      delete next[id]
      return next
    })
  }

  return (
    <div className="page charts-grid-page">
      <div className="page-heading charts-grid-heading">
        <div>
          <div className="page-kicker mono">Binance first / Bybit fallback</div>
          <h1>Charts</h1>
        </div>
        <div className="fx ac gap12 wrap">
          <span className={`chart-stream-status ${connection}`}>{connection}</span>
          <span className="updated mono">{layout.length} / {MAX_CHARTS}</span>
          <button className="btn" type="button" onClick={refreshLayout}>
            <RefreshCw size={16} />Refresh layout
          </button>
        </div>
      </div>
      {dirty && (
        <div className="chart-layout-notice">
          Chart selection changed in another tab. Press <strong>Refresh layout</strong> to apply it.
        </div>
      )}
      {error && <div className="banner">{error}</div>}
      <datalist id="chart-instrument-catalog">
        {catalog.map((instrument) => (
          <option key={instrument.id} value={instrument.symbol}>
            {displayInstrument(instrument)} · {exchangeLabel(instrument.exchange)}
          </option>
        ))}
      </datalist>
      <div className="charts-grid">
        {layout.map((slot) => {
          const instrument = catalogByKey.get(chartInstrumentKey(slot.instrument))
          return (
            <ChartCard
              key={slot.id}
              slot={slot}
              instrument={instrument}
              catalog={catalog}
              data={slotData[slot.id]}
              theme={theme}
              onUpdate={(patch) => updateSlot(slot.id, patch)}
              onRemove={() => removeSlot(slot.id)}
            />
          )
        })}
        {layout.length < MAX_CHARTS && (
          <button className="chart-placeholder" type="button" onClick={addBTC}>
            <span className="chart-placeholder-icon"><Plus size={24} /></span>
            <strong>Add BTC chart</strong>
            <span>New chart uses the 1 hour timeframe.</span>
          </button>
        )}
      </div>
    </div>
  )
}

function ChartCard({ slot, instrument, catalog, data, theme, onUpdate, onRemove }) {
  const containerRef = useRef(null)
  const chartRef = useRef(null)
  const seriesRef = useRef(null)
  const fittedRef = useRef(false)
  const [draft, setDraft] = useState(() => instrument?.symbol || displayStoredInstrument(slot.instrument))
  const [inputError, setInputError] = useState('')

  useEffect(() => {
    setDraft(instrument?.symbol || displayStoredInstrument(slot.instrument))
    setInputError('')
    fittedRef.current = false
  }, [slot.instrument.base, slot.instrument.multiplier, slot.interval, instrument?.symbol])

  useEffect(() => {
    if (!containerRef.current) return undefined
    const styles = getComputedStyle(document.documentElement)
    const text = styles.getPropertyValue('--text').trim()
    const muted = styles.getPropertyValue('--muted').trim()
    const line = styles.getPropertyValue('--line').trim()
    const panel = styles.getPropertyValue('--panel').trim()
    const chart = createChart(containerRef.current, {
      width: containerRef.current.clientWidth,
      height: 340,
      layout: {
        background: { type: ColorType.Solid, color: panel },
        textColor: muted,
        attributionLogo: false,
      },
      grid: {
        vertLines: { color: line },
        horzLines: { color: line },
      },
      crosshair: { mode: CrosshairMode.Normal },
      rightPriceScale: { borderColor: line },
      timeScale: { borderColor: line, timeVisible: true, secondsVisible: false },
      localization: { priceFormatter: formatChartPrice },
    })
    const seriesOptions = {
      upColor: '#1f9d70',
      downColor: '#d45149',
      borderUpColor: '#1f9d70',
      borderDownColor: '#d45149',
      wickUpColor: '#1f9d70',
      wickDownColor: '#d45149',
      priceLineColor: text,
    }
    if (instrument?.tick_size > 0) {
      seriesOptions.priceFormat = {
        type: 'price',
        precision: tickPrecision(instrument.tick_size),
        minMove: instrument.tick_size,
      }
    }
    const series = chart.addSeries(CandlestickSeries, seriesOptions)
    chartRef.current = chart
    seriesRef.current = series
    const observer = new ResizeObserver((entries) => {
      const width = entries[0]?.contentRect?.width
      if (width) chart.applyOptions({ width })
    })
    observer.observe(containerRef.current)
    return () => {
      observer.disconnect()
      chart.remove()
      chartRef.current = null
      seriesRef.current = null
    }
  }, [theme, instrument?.tick_size])

  useEffect(() => {
    if (!seriesRef.current) return
    const candles = (data?.candles || []).map(({ time, open, high, low, close }) => ({ time, open, high, low, close }))
    seriesRef.current.setData(candles)
    if (candles.length && !fittedRef.current) {
      chartRef.current?.timeScale().fitContent()
      fittedRef.current = true
    }
  }, [data?.candles])

  const commitInstrument = () => {
    const resolved = resolveInstrument(draft, catalog)
    if (!resolved) {
      setInputError('Unknown Binance/Bybit perpetual.')
      return
    }
    setInputError('')
    setDraft(resolved.symbol)
    onUpdate({ instrument: { base: resolved.base, multiplier: resolved.multiplier } })
  }
  const onInputKeyDown = (event) => {
    if (event.key === 'Enter') {
      event.preventDefault()
      commitInstrument()
      event.currentTarget.blur()
    }
    if (event.key === 'Escape') {
      setDraft(instrument?.symbol || displayStoredInstrument(slot.instrument))
      setInputError('')
      event.currentTarget.blur()
    }
  }

  return (
    <section className="market-chart-card">
      <div className="market-chart-toolbar">
        <div className="market-chart-symbol">
          <input
            className={`input mono ${inputError ? 'invalid' : ''}`}
            aria-label="Chart coin"
            list="chart-instrument-catalog"
            value={draft}
            onChange={(event) => setDraft(event.target.value.toUpperCase())}
            onBlur={commitInstrument}
            onKeyDown={onInputKeyDown}
          />
          <span className="market-chart-source mono">
            {instrument ? `${exchangeLabel(instrument.exchange)} · ${instrument.quote}` : 'unavailable'}
          </span>
        </div>
        <div className="chart-period" role="group" aria-label="Chart timeframe">
          <button className={slot.interval === '1h' ? 'on' : ''} type="button" onClick={() => onUpdate({ interval: '1h' })}>1H</button>
          <button className={slot.interval === '5m' ? 'on' : ''} type="button" onClick={() => onUpdate({ interval: '5m' })}>5M</button>
        </div>
        <button className="market-chart-remove" type="button" title="Remove chart" aria-label="Remove chart" onClick={onRemove}>
          <X size={16} />
        </button>
      </div>
      <div className="market-chart-meta">
        <span className={`chart-stream-status ${data?.status || 'connecting'}`}>{data?.status || 'waiting'}</span>
        {inputError && <span className="neg">{inputError}</span>}
        {!inputError && data?.error && <span className="neg chart-card-error" title={data.error}>{data.error}</span>}
      </div>
      <div className="market-chart-canvas" ref={containerRef} />
    </section>
  )
}

function mergeCandle(candles, candle) {
  if (!candles.length) return [candle]
  const next = [...candles]
  const last = next[next.length - 1]
  if (last.time === candle.time) next[next.length - 1] = candle
  else if (last.time < candle.time) next.push(candle)
  else {
    const index = next.findIndex((item) => item.time === candle.time)
    if (index >= 0) next[index] = candle
    else next.push(candle)
    next.sort((left, right) => left.time - right.time)
  }
  return next.slice(-300)
}

function resolveInstrument(raw, catalog) {
  const value = String(raw || '').trim().toUpperCase().replace(/[\s/_-]/g, '')
  if (!value) return null
  const exact = catalog.find((item) => (
    String(item.id).toUpperCase() === value ||
    String(item.symbol).toUpperCase().replace(/[\s/_-]/g, '') === value
  ))
  if (exact) return exact
  const candidates = catalog.filter((item) => {
    const prefix = item.multiplier === 1 ? '' : String(item.multiplier)
    return value === item.base ||
      value === `${prefix}${item.base}` ||
      value === `${prefix}${item.base}${item.quote}`
  })
  return candidates.find((item) => item.multiplier === 1) || candidates[0] || null
}

function displayStoredInstrument(instrument) {
  const multiplier = instrument?.multiplier || 1
  return `${multiplier === 1 ? '' : multiplier}${instrument?.base || ''}USDT`
}

function displayInstrument(instrument) {
  const prefix = instrument.multiplier === 1 ? '' : instrument.multiplier
  return `${prefix}${instrument.base}${instrument.quote}`
}

function exchangeLabel(exchange) {
  return exchange === 'binance' ? 'Binance' : exchange === 'bybit' ? 'Bybit' : String(exchange || '')
}

function tickPrecision(tickSize) {
  const text = Number(tickSize).toFixed(12).replace(/0+$/, '')
  const decimal = text.indexOf('.')
  return decimal < 0 ? 0 : text.length - decimal - 1
}

function formatChartPrice(value) {
  const number = Number(value)
  if (!Number.isFinite(number)) return '—'
  if (number >= 1000) return number.toLocaleString(undefined, { maximumFractionDigits: 2 })
  if (number >= 1) return number.toLocaleString(undefined, { maximumFractionDigits: 5 })
  return number.toPrecision(6)
}
