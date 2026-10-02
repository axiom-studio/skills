import { spawn } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';

const RATE = 16000;
const LATENCY = '--latency-msec=20';

function record(device) {
  const child = spawn('parec', ['--device', device, LATENCY, '--format=s16le', `--rate=${RATE}`, '--channels=1', '--raw']);
  const chunks = [];
  child.stdout.on('data', chunk => chunks.push(chunk));
  return { child, chunks };
}

function averageAmplitude(chunks) {
  const data = Buffer.concat(chunks);
  let total = 0;
  for (let offset = 0; offset + 1 < data.length; offset += 2) total += Math.abs(data.readInt16LE(offset));
  return Math.round(total / Math.max(1, data.length / 2));
}

function tone() {
  const pcm = Buffer.alloc(RATE * 2);
  for (let index = 0; index < RATE; index++) {
    pcm.writeInt16LE(Math.round(12000 * Math.sin(2 * Math.PI * 440 * index / RATE)), index * 2);
  }
  return pcm;
}

async function probe(sink, targetSource, isolatedSource) {
  const target = record(targetSource);
  const isolated = record(isolatedSource);
  try {
    await delay(250);
    const player = spawn('pacat', ['--device', sink, LATENCY, '--format=s16le', `--rate=${RATE}`, '--channels=1', '--raw']);
    player.stdin.end(tone());
    await new Promise((resolve, reject) => {
      player.once('error', reject);
      player.once('exit', code => code === 0 ? resolve() : reject(new Error(`pacat exited ${code}`)));
    });
    await delay(120);
  } finally {
    target.child.kill('SIGTERM');
    isolated.child.kill('SIGTERM');
    await delay(80);
  }
  return { target: averageAmplitude(target.chunks), isolated: averageAmplitude(isolated.chunks) };
}

const inbound = await probe('axiom_meet_capture', 'axiom_meet_capture.monitor', 'axiom_bot_microphone.monitor');
const outbound = await probe('axiom_bot_microphone', 'axiom_bot_source', 'axiom_meet_capture.monitor');
process.stdout.write(`${JSON.stringify({ inbound, outbound })}\n`);
if (inbound.target < 500 || outbound.target < 500 || inbound.isolated > 30 || outbound.isolated > 30) {
  process.exitCode = 1;
}
