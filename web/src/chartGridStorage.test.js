import test from 'node:test'
import assert from 'node:assert/strict'
import {
  CHART_GRID_KEY,
  addInstrumentChart,
  chartGridHasInstrument,
  readChartGrid,
  removeChartSlot,
  removeInstrumentCharts,
  updateChartSlot,
} from './chartGridStorage.js'

class MemoryStorage {
  constructor() {
    this.values = new Map()
  }

  getItem(key) {
    return this.values.has(key) ? this.values.get(key) : null
  }

  setItem(key, value) {
    this.values.set(key, String(value))
  }

  clear() {
    this.values.clear()
  }
}

class TestCustomEvent extends Event {
  constructor(type, init = {}) {
    super(type)
    this.detail = init.detail
  }
}

globalThis.localStorage = new MemoryStorage()
globalThis.window = new EventTarget()
globalThis.CustomEvent = TestCustomEvent

test.beforeEach(() => {
  localStorage.clear()
})

test('initializes stable BTC and ETH defaults', () => {
  const first = readChartGrid()
  const second = readChartGrid()
  assert.deepEqual(first, second)
  assert.deepEqual(first.map((chart) => chart.instrument.base), ['BTC', 'ETH'])
  assert.ok(localStorage.getItem(CHART_GRID_KEY))
})

test('persists a timeframe update for a default slot', () => {
  const initial = readChartGrid()
  const updated = updateChartSlot(initial[0].id, { interval: '5m' })
  assert.equal(updated[0].interval, '5m')
  assert.equal(readChartGrid()[0].interval, '5m')
})

test('allows duplicate instruments and enforces the maximum', () => {
  let result = addInstrumentChart({ base: 'BTC', multiplier: 1 })
  assert.equal(result.error, '')
  assert.equal(result.charts.length, 3)
  assert.equal(result.charts.filter((chart) => chart.instrument.base === 'BTC').length, 2)

  for (let index = 0; index < 7; index += 1) {
    result = addInstrumentChart({ base: `COIN${index}`, multiplier: 1 })
  }
  assert.equal(result.charts.length, 10)
  result = addInstrumentChart({ base: 'OVERFLOW', multiplier: 1 })
  assert.equal(result.charts.length, 10)
  assert.match(result.error, /Maximum/)
})

test('removes every slot matching an instrument', () => {
  addInstrumentChart({ base: 'BTC', multiplier: 1 })
  const remaining = removeInstrumentCharts({ base: 'BTC', multiplier: 1 })
  assert.equal(chartGridHasInstrument(remaining, { base: 'BTC', multiplier: 1 }), false)
  assert.equal(chartGridHasInstrument(remaining, { base: 'ETH', multiplier: 1 }), true)
})

test('removes one duplicate slot without removing the others', () => {
  const added = addInstrumentChart({ base: 'BTC', multiplier: 1 }).charts
  const remaining = removeChartSlot(added[0].id)
  assert.equal(remaining.filter((chart) => chart.instrument.base === 'BTC').length, 1)
  assert.equal(remaining.length, 2)
})
