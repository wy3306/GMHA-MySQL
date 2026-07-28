import test from 'node:test'
import assert from 'node:assert/strict'
import { eventDeliveryHistoryFor, eventLabelEntriesFor, normalizeChannel, validateEmailChannel } from './alert-management.js'

test('normalizes QQ SMTP encryption from the selected port', () => {
  const ssl = normalizeChannel({ type: 'email', config: { username: 'sender@example.com', port: '465' } })
  assert.equal(ssl.config.from, 'sender@example.com')
  assert.equal(ssl.config.tls, 'true')
  assert.equal(ssl.config.starttls, 'false')

  const starttls = normalizeChannel({ type: 'email', config: { username: 'sender@example.com', port: '587' } })
  assert.equal(starttls.config.tls, 'false')
  assert.equal(starttls.config.starttls, 'true')
})

test('rejects a mistyped SMTP port before sending', () => {
  assert.throws(
    () => validateEmailChannel({ type: 'email', config: { port: '265' } }),
    /465.*587/
  )
})

test('builds an alert detail view from event labels and matching deliveries', () => {
  const event = { id: 'event-1', labels: { machine_name: 'DB-01', machine_ip: '192.0.2.10' } }
  const deliveries = [
    { id: 'delivery-old', event_id: 'event-1', delivered_at: '2026-07-28T08:00:00Z' },
    { id: 'delivery-other', event_id: 'event-2', delivered_at: '2026-07-28T09:00:00Z' },
    { id: 'delivery-new', event_id: 'event-1', delivered_at: '2026-07-28T10:00:00Z' }
  ]

  assert.deepEqual(eventDeliveryHistoryFor(event, deliveries).map(item => item.id), ['delivery-new', 'delivery-old'])
  assert.deepEqual(eventLabelEntriesFor(event), [
    ['machine_ip', '192.0.2.10'],
    ['machine_name', 'DB-01']
  ])
})
