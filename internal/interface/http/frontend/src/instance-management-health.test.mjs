import test from 'node:test'
import assert from 'node:assert/strict'
import { instanceHealthCode } from './instance-management.js'

test('offline Agent overrides the last successful MySQL heartbeat', () => {
  assert.equal(instanceHealthCode({
    agent_state: 'OFFLINE',
    heartbeat_status: 'OK',
    status: 'running'
  }), 'error')
})

test('stored running state without a current heartbeat is not reported healthy', () => {
  assert.equal(instanceHealthCode({ status: 'running' }), 'unknown')
})

test('a fresh reachable heartbeat reports the instance healthy', () => {
  assert.equal(instanceHealthCode({
    agent_state: 'ONLINE',
    heartbeat_status: 'OK',
    status: 'running'
  }), 'healthy')
})

test('a managed shutdown remains distinct from a failure', () => {
  assert.equal(instanceHealthCode({
    agent_state: 'OFFLINE',
    status: 'stopped'
  }), 'stopped')
})
