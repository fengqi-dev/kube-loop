import test from 'node:test';
import assert from 'node:assert/strict';
import { resourceKey, resolveSplit, resourceAvailability } from './workspace-model.ts';
import { createRequestGeneration } from './request-generation.ts';
test('identity isolates servers, namespaces and resource kinds, including delimiters', () => {
  const base = { profileId: 'one', namespace: 'default', kind: 'pod', id: 'api' };
  const keys = [base, { ...base, profileId: 'two' }, { ...base, namespace: 'other' }, { ...base, kind: 'service' }, { ...base, id: 'api:other' }].map(resourceKey);
  assert.equal(new Set(keys).size, keys.length);
});
test('layout restores valid widths and safely defaults or clamps invalid storage', () => {
  for (const value of [null, undefined, '', 'broken', Infinity]) assert.equal(resolveSplit(value), 55);
  assert.equal(resolveSplit('62'), 62);
  assert.equal(resolveSplit('999'), 75);
  assert.equal(resolveSplit('-1'), 25);
});
test('resource deletion only resolves after a successful inventory response', () => {
  assert.equal(resourceAvailability('a', [], false), 'loading');
  assert.equal(resourceAvailability('a', [], true), 'missing');
  assert.equal(resourceAvailability('a', ['a'], true), 'ready');
});
test('new requests and scope disposal reject stale results', () => {
  const guard = createRequestGeneration();
  const old = guard.begin();
  const current = guard.begin();
  assert.equal(old(), false);
  assert.equal(current(), true);
  guard.invalidate();
  assert.equal(current(), false);
  assert.equal(guard.begin()(), true);
});
