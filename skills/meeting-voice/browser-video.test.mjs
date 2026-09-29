import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { spawn } from 'node:child_process';
import { createBrowserDisplay, openBrowserVideo } from './browser-video.mjs';

function processStub() {
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stdio = [null, child.stdout, null, new PassThrough()];
  child.kills = [];
  child.kill = signal => { child.kills.push(signal); return true; };
  return child;
}

test('virtual display is allocated per process, local-only, and cleaned up', async () => {
  const child = processStub(), controller = new AbortController();
  const ready = createBrowserDisplay({ signal: controller.signal, spawnProcess: (binary, args) => {
    assert.equal(binary, 'Xvfb');
    assert.deepEqual(args.slice(-3), ['-nolisten', 'tcp', '-noreset']);
    return child;
  } });
  child.stdio[3].write('17\n');
  const session = await ready;
  assert.equal(session.display, ':17');
  controller.abort();
  session.close();
  assert.deepEqual(child.kills, ['SIGTERM']);
});

test('invalid display negotiation fails without exposing process diagnostics', async () => {
  const child = processStub();
  const ready = createBrowserDisplay({ spawnProcess: () => child });
  child.stdio[3].write('not-a-display');
  await assert.rejects(ready, /^Error: Browser display is unavailable$/);
  assert.deepEqual(child.kills, ['SIGTERM']);
});

test('video streams encoded binary bytes and cancellation stops the encoder', async () => {
  const child = processStub(), controller = new AbortController();
  const video = openBrowserVideo({ display: ':17', signal: controller.signal,
    spawnProcess: (binary, args, options) => {
      assert.equal(binary, 'ffmpeg');
      assert.ok(args.includes('x11grab'));
      assert.ok(args.includes('libvpx'));
      assert.equal(args.at(-1), 'pipe:1');
      assert.deepEqual(options.stdio, ['ignore', 'pipe', 'ignore']);
      return child;
    } });
  assert.equal(video.mimeType, 'video/webm; codecs="vp8"');
  const iterator = video[Symbol.asyncIterator]();
  const next = iterator.next();
  const header = Buffer.from([0x1a, 0x45, 0xdf, 0xa3]);
  child.stdout.write(header);
  assert.deepEqual((await next).value, header);
  controller.abort();
  await iterator.return();
  assert.deepEqual(child.kills, ['SIGTERM']);
});

test('remote displays and aborted leases cannot start a video encoder', () => {
  const controller = new AbortController();
  for (const display of ['host:0', ':0;cmd', '/tmp/private', undefined]) {
    assert.throws(() => openBrowserVideo({ display, signal: controller.signal }), /unavailable/);
  }
  controller.abort();
  assert.throws(() => openBrowserVideo({ display: ':17', signal: controller.signal }), /unavailable/);
});

test('encoder failure is reported without private subprocess diagnostics', async () => {
  const child = processStub(), controller = new AbortController();
  const video = openBrowserVideo({ display: ':17', signal: controller.signal, spawnProcess: () => child });
  const read = video[Symbol.asyncIterator]().next();
  child.emit('error', new Error('private subprocess detail'));
  await assert.rejects(read, /^Error: Browser video is unavailable$/);
  assert.deepEqual(child.kills, ['SIGTERM']);
});

test('real virtual display produces decodable VP8 video, not image payloads', {
  skip: process.env.BROWSER_VIDEO_INTEGRATION !== '1', timeout: 15000,
}, async () => {
  const controller = new AbortController();
  const display = await createBrowserDisplay({ signal: controller.signal });
  const video = openBrowserVideo({ display: display.display, signal: controller.signal });
  const chunks = [];
  const timer = setTimeout(() => controller.abort(), 2500);
  try {
    for await (const chunk of video) chunks.push(chunk);
    const encoded = Buffer.concat(chunks);
    assert.ok(encoded.length > 100);
    assert.deepEqual(encoded.subarray(0, 4), Buffer.from([0x1a, 0x45, 0xdf, 0xa3]));
    const result = await new Promise((resolve, reject) => {
      const probe = spawn('ffprobe', ['-v', 'error', '-count_frames', '-show_streams', '-of', 'json', 'pipe:0'],
        { stdio: ['pipe', 'pipe', 'ignore'] });
      const output = [];
      probe.stdout.on('data', chunk => output.push(chunk));
      probe.once('error', reject);
      probe.once('close', code => code === 0 ? resolve(JSON.parse(Buffer.concat(output))) : reject(new Error('Video decode failed')));
      probe.stdin.end(encoded);
    });
    assert.equal(result.streams[0].codec_name, 'vp8');
    assert.equal(result.streams[0].width, 1280);
    assert.equal(result.streams[0].height, 800);
    assert.ok(Number(result.streams[0].nb_read_frames) > 2);
  } finally { clearTimeout(timer); video.close(); display.close(); }
});
