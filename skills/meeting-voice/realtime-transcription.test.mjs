import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { RealtimeTranscription, RealtimeCapture } from './realtime-transcription.mjs';
import { TranscriptQueue } from './transcript-queue.mjs';
import WebSocket, { WebSocketServer } from 'ws';
import { once } from 'node:events';

function fixture(options = {}) {
  const socket = new EventEmitter(), sent = [], texts = [], errors = [];
  socket.bufferedAmount = 0;
  socket.send = (text, callback) => { sent.push(JSON.parse(text)); callback(); };
  socket.terminate = () => { socket.terminated = true; socket.emit('close'); };
  const session = new RealtimeTranscription({ apiKey: 'private-key',
    onTranscript: text => texts.push(text), onError: stage => errors.push(stage),
    socketFactory: (url, config) => {
      assert.equal(url.searchParams.get('model_id'), 'scribe_v2_realtime');
      assert.equal(url.searchParams.get('commit_strategy'), 'vad');
      assert.equal(url.searchParams.get('vad_silence_threshold_secs'), '0.5');
      assert.ok(!String(url).includes('private-key'));
      assert.equal(config.headers['xi-api-key'], 'private-key');
      assert.equal(config.followRedirects, false);
      return socket;
    }, ...options,
  });
  const event = value => socket.emit('message', Buffer.from(JSON.stringify(value)));
  return { socket, session, sent, texts, errors, event };
}

test('realtime transcription sends audio before utterance end and publishes only committed text', async () => {
  const f = fixture();
  try {
    f.event({ message_type: 'session_started' });
    await f.session.ready;
    await f.session.send(Buffer.alloc(3200));
    assert.equal(f.sent.length, 1);
    assert.equal(f.sent[0].message_type, 'input_audio_chunk');
    assert.equal(Buffer.from(f.sent[0].audio_base_64, 'base64').length, 3200);
    assert.equal(f.sent[0].commit, undefined, 'provider VAD owns commits');
    f.event({ message_type: 'partial_transcript', text: 'maybe' });
    f.event({ message_type: 'committed_transcript', text: 'Hello team.' });
    f.event({ message_type: 'committed_transcript_with_timestamps', text: 'Hello team.' });
    assert.deepEqual(f.texts, ['Hello team.']);
  } finally { f.session.close(); }
});

test('provider failures are sanitized and terminate once', async () => {
  const f = fixture();
  f.event({ message_type: 'auth_error', error: 'private-key and transcript' });
  await assert.rejects(f.session.ready, /^Error: Realtime transcription closed$/);
  assert.deepEqual(f.errors, ['provider']);
  assert.equal(f.socket.terminated, true);
  f.socket.emit('error', new Error('private-key'));
  assert.equal(f.errors.length, 1);
});

test('bounded provider send buffer and invalid PCM fail explicitly', async () => {
  const f = fixture();
  f.event({ message_type: 'session_started' });
  await assert.rejects(f.session.send(Buffer.alloc(6402)), /invalid/);
  assert.equal(f.sent.length, 0);
  f.socket.bufferedAmount = 64001;
  await assert.rejects(f.session.send(Buffer.alloc(3200)), /behind/);
  assert.deepEqual(f.errors, ['backlog']);
});

test('capture pump sends 100ms frames without waiting for silence', async () => {
  const frames = [], errors = [];
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  const capture = new RealtimeCapture({ signal: new AbortController().signal,
    send: async pcm => { frames.push(pcm); if (frames.length === 1) await pending; }, onError: error => errors.push(error) });
  capture.feed(Buffer.alloc(6400, 1));
  assert.equal(frames.length, 1);
  assert.equal(frames[0].length, 3200);
  capture.feed(Buffer.alloc(1600, 2));
  release();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(frames.length, 2);
  assert.equal(capture.pending.length, 1600);
  assert.deepEqual(errors, []);
});

test('capture backlog stops explicitly without unbounded memory or duplicate errors', async () => {
  const errors = [], controller = new AbortController();
  let release;
  const capture = new RealtimeCapture({ signal: controller.signal,
    send: () => new Promise(resolve => { release = resolve; }), onError: error => errors.push(error) });
  capture.feed(Buffer.alloc(3200));
  capture.feed(Buffer.alloc(64001));
  capture.feed(Buffer.alloc(3200));
  assert.deepEqual(errors, ['realtime_capture_backlog']);
  assert.equal(capture.pending.length, 0);
  release();
});

test('committed transcripts enter canonical chat queue without second transcription', async () => {
  const posted = [];
  const queue = new TranscriptQueue({ signal: new AbortController().signal,
    transcribe: () => assert.fail('must not transcribe text again'),
    postUtterance: async text => { posted.push(text); return { id: text }; },
    reply: async () => {}, onError: () => assert.fail('unexpected error') });
  queue.enqueueText(' Hello ');
  queue.enqueueText('Team');
  await queue.drain();
  assert.deepEqual(posted, ['Hello', 'Team']);
  assert.equal(queue.pendingBytes, 0);
});

test('real WebSocket delivers PCM and committed transcripts without a batch upload', async () => {
  const server = new WebSocketServer({ host: '127.0.0.1', port: 0 });
  await once(server, 'listening');
  let receivedBytes = 0, complete;
  const transcript = new Promise(resolve => { complete = resolve; });
  server.on('connection', (socket, request) => {
    assert.equal(request.headers['xi-api-key'], 'test-only');
    socket.send(JSON.stringify({ message_type: 'session_started' }));
    socket.on('message', data => {
      const message = JSON.parse(data);
      receivedBytes += Buffer.from(message.audio_base_64, 'base64').length;
      socket.send(JSON.stringify({ message_type: 'partial_transcript', text: 'unfinished' }));
      socket.send(JSON.stringify({ message_type: 'committed_transcript', text: 'Hello team.' }));
    });
  });
  const session = new RealtimeTranscription({ apiKey: 'test-only',
    socketFactory: (_, options) => new WebSocket(`ws://127.0.0.1:${server.address().port}`, options),
    onTranscript: complete, onError: () => assert.fail('unexpected websocket error') });
  try {
    await session.ready;
    await session.send(Buffer.alloc(3200));
    assert.equal(await transcript, 'Hello team.');
    assert.equal(receivedBytes, 3200);
  } finally {
    session.close();
    for (const socket of server.clients) socket.terminate();
    await new Promise(resolve => server.close(resolve));
  }
});
