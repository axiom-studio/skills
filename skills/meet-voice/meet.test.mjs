import assert from 'node:assert/strict';
import { test } from 'node:test';
import { meetingURL, joinMeet } from './meet.mjs';

test('accepts direct Meet links only', () => {
  assert.equal(meetingURL('https://meet.google.com/abc-defg-hij'), 'https://meet.google.com/abc-defg-hij');
  for (const url of [
    'https://meet.google.com.evil.test/abc-defg-hij',
    'http://meet.google.com/abc-defg-hij',
    'https://meet.google.com:444/abc-defg-hij',
    'https://user:pass@meet.google.com/abc-defg-hij',
    'https://meet.google.com/abc-defg-hij?tracking=1',
    'https://meet.google.com/abc-defg-hij#fragment',
    'https://meet.google.com/ABC-DEFG-HIJ',
    'https://meet.google.com/not-a-meeting',
  ]) assert.throws(() => meetingURL(url));
});

test('closes the browser if admission fails', async () => {
  let closed = false;
  const button = { waitFor: async () => {}, click: async () => {}, isVisible: async () => false };
  const page = {
    goto: async () => {},
    getByRole(_role, { name }) {
      if (name.test('Leave call')) return { waitFor: async () => { throw new Error('admission timed out'); } };
      return button;
    },
  };
  const chromiumAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => { closed = true; } }) };
  await assert.rejects(joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile', chromiumAPI }), /admission timed out/);
  assert.equal(closed, true);
});

test('fills a visible guest name before requesting admission', async () => {
  const actions = [];
  const button = { waitFor: async () => {}, click: async () => { actions.push('join'); }, isVisible: async () => false };
  const page = {
    goto: async () => {},
    getByRole(role, { name }) {
      if (role === 'textbox') return { isVisible: async () => true, fill: async value => { actions.push(value); } };
      if (name.test('Leave call')) return { waitFor: async () => {}, click: async () => {} };
      return button;
    },
  };
  const chromiumAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  const meeting = await joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile',
    displayName: 'Axiom Meeting Agent', chromiumAPI });
  assert.deepEqual(actions, ['Axiom Meeting Agent', 'join']);
  await meeting.leave();
});

test('reports host admission only after requesting it', async () => {
  const actions = [];
  const page = {
    goto: async () => {},
    getByRole(role, { name }) {
      if (role === 'textbox') return { isVisible: async () => false };
      if (name.test('Ask to join') && !name.test('Join now')) return { isVisible: async () => true };
      if (name.test('Leave call')) return { waitFor: async () => {}, click: async () => {} };
      return { waitFor: async () => {}, click: async () => { actions.push('requested'); }, isVisible: async () => false };
    },
  };
  const chromiumAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  const meeting = await joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile',
    chromiumAPI, onAdmissionRequested: () => actions.push('waiting') });
  assert.deepEqual(actions, ['requested', 'waiting']);
  await meeting.leave();
});
