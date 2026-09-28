import assert from 'node:assert/strict';
import { test } from 'node:test';
import { meetingPlatform, meetingURL, joinMeet as realJoinMeet } from './meet.mjs';

async function joinMeet(options) {
  const launch = options.chromiumAPI.launchPersistentContext;
  return realJoinMeet({ ...options, chromiumAPI: { async launchPersistentContext(...args) {
    const context = await launch(...args);
    for (const page of context.pages()) {
      if (!page.getByRole) continue;
      const getByRole = page.getByRole.bind(page);
      page.getByRole = (...selector) => {
        const locator = getByRole(...selector);
        locator.first ??= () => locator;
        return locator;
      };
    }
    return context;
  } } });
}

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

test('accepts direct Zoom and Teams links without allowing arbitrary hosts', () => {
  assert.equal(meetingPlatform('https://us06web.zoom.us/j/12345678901?pwd=abc123'), 'zoom');
  assert.equal(meetingPlatform('https://teams.microsoft.com/l/meetup-join/19%3ameeting_abc%40thread.v2/0?context=%7B%7D'), 'teams');
  for (const url of [
    'https://zoom.us.evil.test/j/12345678901',
    'https://zoom.us/j/12345678901?redirect=https://evil.test',
    'https://teams.microsoft.com.evil.test/l/meetup-join/19%3ameeting_abc%40thread.v2/0',
    'https://teams.microsoft.com/l/chat/not-a-meeting',
  ]) assert.throws(() => meetingURL(url));
});

test('joins a Zoom browser meeting with a visible guest name', async () => {
  const actions = [];
  const leave = { waitFor: async () => {}, click: async () => {}, isVisible: async () => true };
  const page = {
    goto: async () => {},
    getByRole(role, { name }) {
      if (role === 'link') return { waitFor: async () => {}, click: async () => { actions.push('browser'); } };
      if (role === 'textbox') return { isVisible: async () => true, fill: async value => { actions.push(value); } };
      if (name.test('Leave')) return leave;
      return { waitFor: async () => {}, click: async () => { actions.push('join'); } };
    },
    getByText: () => ({ isVisible: async () => false }),
  };
  const chromiumAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  const meeting = await joinMeet({ url: 'https://zoom.us/j/12345678901', profileDir: '/profile',
    displayName: 'Quorum', chromiumAPI });
  assert.equal(meeting.platform, 'zoom');
  assert.deepEqual(actions, ['browser', 'Quorum', 'join']);
  await meeting.leave();
});

test('joins Teams through the browser and waits for admission', async () => {
  const actions = [];
  const leave = { waitFor: async () => {}, click: async () => {}, isVisible: async () => true };
  const page = {
    goto: async () => {},
    getByRole(role, { name }) {
      if (role === 'textbox') return { isVisible: async () => true, fill: async value => { actions.push(value); } };
      if (name.test('Leave')) return leave;
      return { waitFor: async () => {}, isVisible: async () => true, click: async () => { actions.push('click'); } };
    },
    getByText: () => ({ isVisible: async () => true }),
  };
  const chromiumAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  const meeting = await joinMeet({ url: 'https://teams.microsoft.com/l/meetup-join/19%3ameeting_abc%40thread.v2/0',
    profileDir: '/profile', displayName: 'Quorum', chromiumAPI,
    onAdmissionRequested: () => actions.push('waiting') });
  assert.equal(meeting.platform, 'teams');
  assert.deepEqual(actions, ['click', 'Quorum', 'click', 'waiting', 'click']);
  await meeting.leave();
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
  await assert.rejects(joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile', chromiumAPI }), /did not confirm admission/);
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

test('waits for the asynchronously rendered prejoin form before filling the guest name', async () => {
  let ready = false;
  let filled = false;
  const page = {
    goto: async () => {},
    getByRole(role, { name }) {
      if (role === 'textbox') return {
        isVisible: async () => ready,
        fill: async value => { assert.equal(value, 'Meet Swift'); filled = true; },
      };
      if (name.test('Leave call')) return { waitFor: async () => {}, click: async () => {} };
      if (name.test('Join now')) return {
        waitFor: async () => { ready = true; },
        click: async () => { assert.equal(filled, true, 'join clicked with an empty guest name'); },
      };
      return { isVisible: async () => false };
    },
  };
  const chromiumAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  const meeting = await joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile',
    displayName: 'Meet Swift', chromiumAPI });
  await meeting.leave();
});

test('join diagnostics identify the failed stage without exposing browser error content', async () => {
  const page = { goto: async () => { throw new Error('private page body and secret'); } };
  const chromiumAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  await assert.rejects(joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile', chromiumAPI }), error => {
    assert.match(error.message, /meeting page could not be loaded/);
    assert.doesNotMatch(error.message, /private|secret/);
    return true;
  });
});

test('dismisses the initial media dialog before entering a guest name and joining', async () => {
  const actions = [];
  let dialog = true;
  const page = {
    goto: async () => {},
    getByRole(role, { name }) {
      if (role === 'textbox') return {
        isVisible: async () => !dialog,
        fill: async value => actions.push(value),
      };
      if (name.test('Continue without microphone and camera') && !name.test('Join now')) return {
        isVisible: async () => dialog,
        click: async () => { dialog = false; actions.push('dismiss media'); },
      };
      if (name.test('Join now')) return {
        waitFor: async () => {
          if (!name.test('Continue without microphone and camera')) assert.equal(dialog, false);
        },
        click: async () => { assert.equal(dialog, false); actions.push('join'); },
      };
      if (name.test('Leave call')) return { waitFor: async () => {}, click: async () => {} };
      return { isVisible: async () => false };
    },
  };
  const chromiumAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  const meeting = await joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile',
    displayName: 'Meet Swift', chromiumAPI });
  assert.deepEqual(actions, ['dismiss media', 'Meet Swift', 'join']);
  await meeting.leave();
});
