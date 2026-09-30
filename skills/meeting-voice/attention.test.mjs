import test from 'node:test';
import assert from 'node:assert/strict';
import { isAddressed, attributeSpeaker } from './attention.mjs';

test('wake phrases match whole Unicode words without regex interpolation', () => {
  assert.equal(isAddressed('Hey, Meet Swift! Can you help?', ['Meet Swift']), true);
  assert.equal(isAddressed('meeting swiftly finishes', ['Meet Swift']), false);
  assert.equal(isAddressed('agentless', ['agent']), false);
  assert.equal(isAddressed('hello', ['']), false);
  assert.equal(isAddressed('नमस्ते साथी', ['साथी']), true);
  assert.equal(isAddressed('Computer, take a note', ['Meet Swift', 'computer']), true);
});

test('speaker evidence is time bounded and ambiguity stays unknown', () => {
  assert.equal(attributeSpeaker([{ at: 2, speakers: ['Kevin'] }], 1, 3), 'Kevin');
  for (const samples of [[], [{ at: 4, speakers: ['Kevin'] }],
    [{ at: 2, speakers: ['Kevin', 'Vishnu'] }],
    [{ at: 1, speakers: ['Kevin'] }, { at: 2, speakers: ['Vishnu'] }],
    [{ at: 2, speakers: [] }]]) {
    assert.equal(attributeSpeaker(samples, 1, 3), 'Unknown speaker');
  }
});
