export const CHART_GRID_KEY = 'scalpAssist.chartGrid.v1'
export const CHART_GRID_EVENT = 'scalp-assist:charts-changed'
export const MAX_CHARTS = 10

const DEFAULT_INTERVAL = '1h'

function slotID() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID()
  return `chart-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function normalizeInstrument(instrument = {}) {
  const base = String(instrument.base || instrument.Base || '').trim().toUpperCase()
  const multiplier = Number(instrument.multiplier || instrument.Multiplier || 1)
  if (!base || !/^[A-Z0-9]+$/.test(base) || !Number.isInteger(multiplier) || multiplier <= 0) return null
  return { base, multiplier }
}

function normalizeCharts(charts) {
  if (!Array.isArray(charts)) return null
  const ids = new Set()
  const normalized = []
  for (const item of charts) {
    const instrument = normalizeInstrument(item?.instrument)
    if (!instrument) continue
    let id = String(item?.id || '').trim()
    if (!id || ids.has(id)) id = slotID()
    ids.add(id)
    normalized.push({
      id,
      instrument,
      interval: item?.interval === '5m' ? '5m' : DEFAULT_INTERVAL,
    })
    if (normalized.length === MAX_CHARTS) break
  }
  return normalized
}

function defaultCharts() {
  return [
    { id: slotID(), instrument: { base: 'BTC', multiplier: 1 }, interval: DEFAULT_INTERVAL },
    { id: slotID(), instrument: { base: 'ETH', multiplier: 1 }, interval: DEFAULT_INTERVAL },
  ]
}

function initializeDefaults() {
  const charts = defaultCharts()
  localStorage.setItem(CHART_GRID_KEY, JSON.stringify({ version: 1, charts }))
  return charts
}

export function readChartGrid() {
  try {
    const raw = localStorage.getItem(CHART_GRID_KEY)
    if (raw == null) return initializeDefaults()
    const parsed = JSON.parse(raw)
    if (parsed?.version !== 1) return initializeDefaults()
    return normalizeCharts(parsed.charts) ?? initializeDefaults()
  } catch {
    return initializeDefaults()
  }
}

export function writeChartGrid(charts) {
  const normalized = normalizeCharts(charts) ?? []
  localStorage.setItem(CHART_GRID_KEY, JSON.stringify({ version: 1, charts: normalized }))
  window.dispatchEvent(new CustomEvent(CHART_GRID_EVENT, { detail: normalized }))
  return normalized
}

export function updateChartGrid(mutator) {
  const current = readChartGrid()
  return writeChartGrid(mutator([...current]) || current)
}

export function chartInstrumentKey(instrument = {}) {
  const normalized = normalizeInstrument(instrument)
  return normalized ? `${normalized.base}:${normalized.multiplier}` : ''
}

export function chartGridHasInstrument(charts, instrument) {
  const key = chartInstrumentKey(instrument)
  return Boolean(key && charts.some((chart) => chartInstrumentKey(chart.instrument) === key))
}

export function addInstrumentChart(instrument, interval = DEFAULT_INTERVAL) {
  const normalized = normalizeInstrument(instrument)
  if (!normalized) return { charts: readChartGrid(), error: 'Unknown chart instrument.' }
  const current = readChartGrid()
  if (current.length >= MAX_CHARTS) return { charts: current, error: 'Maximum of 10 charts reached.' }
  return {
    charts: writeChartGrid([...current, {
      id: slotID(),
      instrument: normalized,
      interval: interval === '5m' ? '5m' : DEFAULT_INTERVAL,
    }]),
    error: '',
  }
}

export function removeInstrumentCharts(instrument) {
  const key = chartInstrumentKey(instrument)
  return writeChartGrid(readChartGrid().filter((chart) => chartInstrumentKey(chart.instrument) !== key))
}

export function updateChartSlot(id, patch) {
  return updateChartGrid((charts) => charts.map((chart) => {
    if (chart.id !== id) return chart
    return {
      ...chart,
      ...patch,
      instrument: patch.instrument || chart.instrument,
    }
  }))
}

export function removeChartSlot(id) {
  return writeChartGrid(readChartGrid().filter((chart) => chart.id !== id))
}

export function subscribeChartGrid(listener) {
  const onStorage = (event) => {
    if (event.key === CHART_GRID_KEY) listener(readChartGrid(), 'storage')
  }
  const onLocal = (event) => listener(event.detail || readChartGrid(), 'local')
  window.addEventListener('storage', onStorage)
  window.addEventListener(CHART_GRID_EVENT, onLocal)
  return () => {
    window.removeEventListener('storage', onStorage)
    window.removeEventListener(CHART_GRID_EVENT, onLocal)
  }
}
