import test from 'node:test'
import assert from 'node:assert/strict'
import { alertRuleCategory, channelContentSummary, channelEnabledRequest, channelRecipientLabels, channelWithEnabled, eventDeliveryHistoryFor, eventLabelEntriesFor, normalizeChannel, validateEmailChannel } from './alert-management.js'

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

test('changes only the selected channel enabled state without mutating its saved data', () => {
  const channel = { id: 'ding-1', name: '值班群', type: 'dingtalk', enabled: true, minimum_severity: 'warning', config: { webhook: '******' } }
  const disabled = channelWithEnabled(channel, false)

  assert.equal(disabled.enabled, false)
  assert.equal(disabled.id, channel.id)
  assert.deepEqual(disabled.config, channel.config)
  assert.equal(channel.enabled, true)
  assert.deepEqual(channelEnabledRequest(channel.id, false), {
    method: 'PATCH',
    body: JSON.stringify({ id: channel.id, enabled: false })
  })
})

test('normalizes channel routing and summarizes platform users and roles without exposing email addresses', () => {
  const legacy = normalizeChannel({ type: 'dingtalk', config: { webhook: '******' } })
  assert.deepEqual(legacy.recipient_ids, [])
  assert.deepEqual(legacy.recipient_roles, [])
  assert.deepEqual(legacy.content_filter, { categories: [], event_states: ['firing', 'resolved'] })
  assert.deepEqual(channelRecipientLabels(legacy), ['群组 / 系统接收端'])
  assert.equal(channelContentSummary(legacy), '全部告警内容 · 触发与恢复')

  const routed = normalizeChannel({
    type: 'email',
    recipient_ids: ['user-1'], recipient_roles: ['dba'],
    content_filter: { categories: ['replication', 'storage'], event_states: ['firing'] }, config: {}
  })
  const directory = { users: [{ id: 'user-1', name: '张三', username: 'zhangsan' }], roles: [{ id: 'dba', name: 'DBA' }] }
  assert.deepEqual(channelRecipientLabels(routed, directory), ['角色：DBA', '张三'])
  assert.equal(channelContentSummary(routed), '复制高可用、存储容量 · 告警触发')
  assert.doesNotThrow(() => validateEmailChannel({ type: 'email', recipient_ids: ['user-1'], recipient_roles: [], config: { port: '465' } }))
  assert.throws(() => validateEmailChannel({ type: 'email', recipient_ids: [], recipient_roles: [], config: { port: '465' } }), /推送人员.*平台角色/)
})

test('builds an alert detail view from event labels and matching deliveries', () => {
  const event = { id: 'event-1', labels: { machine_name: 'DB-01', machine_ip: '192.0.2.10', database: 'orders' } }
  const deliveries = [
    { id: 'delivery-old', event_id: 'event-1', delivered_at: '2026-07-28T08:00:00Z' },
    { id: 'delivery-other', event_id: 'event-2', delivered_at: '2026-07-28T09:00:00Z' },
    { id: 'delivery-new', event_id: 'event-1', delivered_at: '2026-07-28T10:00:00Z' }
  ]

  assert.deepEqual(eventDeliveryHistoryFor(event, deliveries).map(item => item.id), ['delivery-new', 'delivery-old'])
  assert.deepEqual(eventLabelEntriesFor(event), [
    ['database', 'orders']
  ])
})

test('classifies built-in alert metrics into every visible rule category', () => {
  const samples = {
    host: 'cpu_usage_percent',
    agent: 'agent_heartbeat_alive',
    availability: 'mysql_connectivity',
    connection: 'mysql_connection_usage_percent',
    replication: 'mysql_replication_lag',
    transaction: 'mysql_longest_transaction_seconds',
    performance: 'mysql_slowest_sql_seconds',
    storage: 'mysql_data_disk_usage',
    fragmentation: 'mysql_fragmented_table_count',
    log: 'mysql_recent_error_count'
  }
  for (const [category, metric] of Object.entries(samples)) {
    assert.equal(alertRuleCategory(metric), category, `${metric} should belong to ${category}`)
  }
  assert.equal(alertRuleCategory('mysql_max_table_fragment_percent'), 'fragmentation')
  assert.equal(alertRuleCategory('mysql_tablespace_fragment_total_bytes'), 'fragmentation')
})
