import assert from 'node:assert/strict';
import { test } from 'node:test';
import { meetingPlatform, meetingURL, joinMeet as realJoinMeet } from './meet.mjs';
import { BrowserHandoff } from './browser-handoff.mjs';

async function joinMeet(options) {
  const launch = options.browserAPI.launchPersistentContext;
  return realJoinMeet({ ...options, browserAPI: { async launchPersistentContext(...args) {
    const context = await launch(...args);
    for (const page of context.pages()) {
      page.getByText ??= () => ({ waitFor: () => new Promise(() => {}) });
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

function zoomFixture(initial = 'landing') {
  const actions = [];
  let state = initial;
  let closed = false;
  let currentURL = 'https://app.zoom.us/wc/12345678901/join';
  const locator = (shown, click = async () => {}) => ({
    first() { return this; }, isVisible: async () => shown(), click,
    fill: async value => actions.push(value),
  });
  const page = {
    goto: async () => { actions.push('navigate'); },
    url: () => currentURL,
    isClosed: () => closed,
    getByRole(role, { name }) {
      if (role === 'link') return locator(() => state === 'landing', async () => { actions.push('browser'); state = 'prejoin'; });
      if (role === 'textbox') return locator(() => state === 'prejoin');
      if (name.test('Leave')) return locator(() => ['active', 'waiting'].includes(state));
      if (name.test('Join')) return locator(() => state === 'prejoin', async () => { actions.push('join'); state = 'active'; });
      return locator(() => false);
    },
    getByText: pattern => locator(() => state === 'waiting' ? pattern.test('waiting for host') : state === 'connecting' && pattern.test('Joining...')),
  };
  const options = { url: 'https://zoom.us/j/12345678901', profileDir: '/profile', displayName: 'Quorum', timeoutMs: 1000,
    browserAPI: { launchPersistentContext: async () => ({ pages: () => [page], close: async () => { closed = true; } }) } };
  return { actions, options, setState: value => { state = value; }, setURL: value => { currentURL = value; }, closed: () => closed };
}

test('joins a Zoom browser meeting with a visible guest name', async () => {
  const { actions, options } = zoomFixture();
  const meeting = await joinMeet(options);
  assert.equal(meeting.platform, 'zoom');
  assert.deepEqual(actions, ['navigate', 'browser', 'Quorum', 'join']);
  await meeting.leave();
});

for (const state of ['connecting', 'waiting', 'active', 'prejoin']) {
  test(`Zoom handoff resumes ${state} without reopening the meeting`, async () => {
    const f = zoomFixture();
    let admissions = 0;
    const meeting = await joinMeet({ ...f.options, handoffBeforeJoin: true,
      onAdmissionRequested: () => { admissions++; },
      handoff: { request: async () => {
        f.setState(state);
        if (['waiting', 'connecting'].includes(state)) setTimeout(() => f.setState('active'), 30);
      } },
    });
    assert.deepEqual(f.actions, state === 'prejoin' ? ['navigate', 'Quorum', 'join'] : ['navigate']);
    assert.equal(admissions, state === 'waiting' ? 1 : 0);
    await meeting.leave();
  });
}

test('Zoom timeout identifies connecting stage without leaking browser content', async () => {
  const f = zoomFixture('connecting');
  await assert.rejects(joinMeet({ ...f.options, timeoutMs: 15 }), /Zoom remained on its connecting screen/);
  assert.equal(f.closed(), true);
});

test('Zoom waiting room Leave button does not falsely confirm admission', async () => {
  const f = zoomFixture('waiting');
  await assert.rejects(joinMeet({ ...f.options, timeoutMs: 15 }), /host admission/);
});

for (const url of ['https://zoom.us/signin', 'https://evil.test/wc/12345678901/join', 'https://app.zoom.us/wc/99999999999/join']) {
  test(`Zoom handoff refuses unrelated page: ${url}`, async () => {
    const f = zoomFixture();
    await assert.rejects(joinMeet({ ...f.options, handoffBeforeJoin: true,
      handoff: { request: async () => { f.setURL(url); } },
    }), /return to the authorized Zoom meeting/);
    assert.deepEqual(f.actions, ['navigate']);
    assert.equal(f.closed(), true);
  });
}

test('automatic Zoom handoff also resumes without replaying navigation', async () => {
  const f = zoomFixture('unknown');
  const meeting = await joinMeet({ ...f.options, timeoutMs: 15,
    interventionReason: async () => 'authentication',
    handoff: { request: async () => f.setState('active') },
  });
  assert.deepEqual(f.actions, ['navigate']);
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
  const browserAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  const meeting = await joinMeet({ url: 'https://teams.microsoft.com/l/meetup-join/19%3ameeting_abc%40thread.v2/0',
    profileDir: '/profile', displayName: 'Quorum', browserAPI,
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
  const browserAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => { closed = true; } }) };
  await assert.rejects(joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile', browserAPI }), /did not confirm admission/);
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
  const browserAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  const meeting = await joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile',
    displayName: 'Axiom Meeting Agent', browserAPI });
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
  const browserAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  const meeting = await joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile',
    browserAPI, onAdmissionRequested: () => actions.push('waiting') });
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
  const browserAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  const meeting = await joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile',
    displayName: 'Meet Swift', browserAPI });
  await meeting.leave();
});

test('join diagnostics identify the failed stage without exposing browser error content', async () => {
  const page = { goto: async () => { throw new Error('private page body and secret'); } };
  const browserAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  await assert.rejects(joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile', browserAPI }), error => {
    assert.match(error.message, /meeting page could not be loaded/);
    assert.doesNotMatch(error.message, /private|secret/);
    return true;
  });
});

for (const mediaLabel of ['Continue without microphone and camera', 'Continue without microphone']) {
test(`dismisses ${mediaLabel} before entering a guest name and joining`, async () => {
  const actions = [];
  let dialog = true;
  const page = {
    goto: async () => {},
    getByRole(role, { name }) {
      if (role === 'textbox') return {
        isVisible: async () => !dialog,
        fill: async value => actions.push(value),
      };
      if (name.test(mediaLabel) && !name.test('Join now')) return {
        isVisible: async () => dialog,
        click: async () => { dialog = false; actions.push('dismiss media'); },
      };
      if (name.test('Join now')) return {
        waitFor: async () => {
          if (!name.test(mediaLabel)) assert.equal(dialog, false);
        },
        click: async () => { assert.equal(dialog, false); actions.push('join'); },
      };
      if (name.test('Leave call')) return { waitFor: async () => {}, click: async () => {} };
      return { isVisible: async () => false };
    },
  };
  const browserAPI = { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) };
  const meeting = await joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile',
    displayName: 'Meet Swift', browserAPI });
  assert.deepEqual(actions, ['dismiss media', 'Meet Swift', 'join']);
  await meeting.leave();
});
}

test('reports a Meet access refusal immediately without attempting to join', async () => {
  let closed = false;
  let clicked = false;
  const page = {
    goto: async () => {},
    getByRole: () => ({
      waitFor: () => new Promise(() => {}),
      click: async () => { clicked = true; },
    }),
    getByText: (text, options) => {
      assert.equal(text, "You can't join this video call");
      assert.equal(options.exact, true);
      return { waitFor: async () => {} };
    },
  };
  const browserAPI = { launchPersistentContext: async () => ({
    pages: () => [page], close: async () => { closed = true; },
  }) };
  await assert.rejects(joinMeet({ url: 'https://meet.google.com/abc-defg-hij',
    profileDir: '/profile', browserAPI }), /Google Meet refused access.*host.*guest access/);
  assert.equal(clicked, false);
  assert.equal(closed, true);
});

test('a failed join without a human-only blocker does not offer handoff', async () => {
  let closed = false;
  let requested = false;
  const page = {
    goto: async () => {},
    getByRole: () => ({ waitFor: () => new Promise(() => {}) }),
    getByText: () => ({ waitFor: async () => {} }),
  };
  await assert.rejects(joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile',
    handoff: { request: async () => { requested = true; } },
    interventionReason: async () => undefined,
    browserAPI: { launchPersistentContext: async () => ({ pages: () => [page], close: async () => { closed = true; } }) },
  }), /Google Meet refused access/);
  assert.equal(requested, false);
  assert.equal(closed, true);
});

for (const handoffBeforeJoin of [false, true]) {
test(`handoff retains the same browser and confirms manual admission (explicit setup: ${handoffBeforeJoin})`, async () => {
  let admitted = false;
  let launches = 0;
  let closed = 0;
  let navigations = 0;
  const principal = { tenantID: 't1', agentID: 'a1', userID: 'u1' };
  let offered;
  const ready = new Promise(resolve => { offered = resolve; });
  const handoff = new BrowserHandoff({ ...principal, onState: state => { if (state === 'awaiting_user') offered(); } });
  const page = {
    goto: async () => { navigations++; },
    getByRole: (_role, { name }) => name.test('Leave call')
      ? { isVisible: async () => admitted, click: async () => {} }
      : { waitFor: () => new Promise(() => {}), isVisible: async () => false },
    getByText: () => ({ waitFor: async () => {} }),
    viewportSize: () => ({ width: 1280, height: 800 }),
    mouse: { click: async () => { admitted = true; } },
  };
  const context = { pages: () => [page], close: async () => { closed++; } };
  const joining = joinMeet({ url: 'https://meet.google.com/abc-defg-hij', profileDir: '/profile', handoff,
    handoffBeforeJoin,
    interventionReason: async () => 'authentication',
    browserAPI: { launchPersistentContext: async () => { launches++; return context; } } });
  await ready;
  assert.equal(closed, 0);
  const lease = await handoff.handle(principal, { type: 'claim' });
  await handoff.handle(principal, { type: 'input', leaseID: lease.id, input: { type: 'click', x: 10, y: 10 } });
  await handoff.handle(principal, { type: 'resume', leaseID: lease.id });
  const meeting = await joining;
  assert.equal(meeting.page, page);
  assert.equal(meeting.context, context);
  assert.equal(await meeting.isPresent(), true);
  assert.equal(launches, 1);
  assert.equal(navigations, 1);
  await meeting.leave();
  assert.equal(closed, 1);
});
}

test('returning from sign-in reopens only the approved meeting once, without reading login fields', async () => {
  let signedIn = false;
  let admission = false;
  const navigations = [];
  const principal = { tenantID: 't1', agentID: 'a1', userID: 'u1' };
  let offered;
  const ready = new Promise(resolve => { offered = resolve; });
  const handoff = new BrowserHandoff({ ...principal, onState: state => { if (state === 'awaiting_user') offered(); } });
  const page = {
    goto: async target => { navigations.push(target); },
    getByRole: (role, { name }) => {
      if (role === 'textbox') return { isVisible: async () => false };
      if (name.test('Leave call')) return { isVisible: async () => admission, waitFor: async () => { assert.equal(admission, true); }, click: async () => {} };
      if (name.test('Join now')) return {
        waitFor: () => signedIn ? Promise.resolve() : new Promise(() => {}),
        click: async () => { assert.equal(navigations.length, 2); admission = true; },
        isVisible: async () => false,
      };
      return { isVisible: async () => false };
    },
    getByText: () => ({ waitFor: () => signedIn ? new Promise(() => {}) : Promise.resolve() }),
    viewportSize: () => ({ width: 1280, height: 800 }),
    mouse: { click: async () => { signedIn = true; } },
  };
  const target = 'https://meet.google.com/abc-defg-hij';
  const joining = joinMeet({ url: target, profileDir: '/profile', handoff,
    interventionReason: async () => 'authentication',
    browserAPI: { launchPersistentContext: async () => ({ pages: () => [page], close: async () => {} }) } });
  await ready;
  const lease = await handoff.handle(principal, { type: 'claim' });
  await handoff.handle(principal, { type: 'input', leaseID: lease.id, input: { type: 'click', x: 10, y: 10 } });
  await handoff.handle(principal, { type: 'resume', leaseID: lease.id });
  const meeting = await joining;
  assert.deepEqual(navigations, [target, target]);
  assert.equal(await meeting.isPresent(), true);
  await meeting.leave();
});
