// Host-only transport. Legacy Video accepts proofs and emits encoded video;
// Desktop multiplexes signed proofs and native input, emitting RFB + barriers.
export function browserVideoRPC(service, call, desktop = false) {
  let session, closed = false;
  const close = () => { if (closed) return; closed = true; session?.close(); };
  const fail = () => {
    close();
    call.destroy(Object.assign(new Error('Browser video is unavailable'), { code: 7 }));
  };
  call.once('cancelled', close);
  call.once('close', close);
  call.once('error', close);
  call.once('end', close);
  const pump = async () => {
    try {
      for await (const bytes of session.stream) {
        if (closed) break;
        if (!call.write({ value: desktop ? Buffer.concat([Buffer.from([1]), bytes]) : bytes })) await new Promise((resolve, reject) => {
          const cleanup = () => { call.off('drain', drained); call.off('close', stopped); call.off('cancelled', stopped); };
          const drained = () => { cleanup(); resolve(); };
          const stopped = () => { cleanup(); reject(new Error()); };
          call.once('drain', drained); call.once('close', stopped); call.once('cancelled', stopped);
          if (closed) stopped();
        });
      }
      close();
      call.end();
    } catch { fail(); }
  };
  call.on('data', async packet => {
    call.pause();
    try {
      if (closed || !packet?.value || packet.value.length > (desktop ? 65537 : 49152)) throw new Error();
      let bytes = Buffer.from(packet.value);
      if (desktop) {
        const kind = bytes[0]; bytes = bytes.subarray(1);
        // Ordered after all preceding input writes, including worker ACKs.
        // The client waits for this barrier before explicit return control.
        if (kind === 2 && session && bytes.length === 0) {
          // A full send buffer is backpressure, not failure: gRPC queues the
          // barrier in order behind the frames already written.
          call.write({ value: Buffer.from([2]) });
          if (!closed) call.resume();
          return;
        }
        if (kind === 1 && session) {
          await session.write(bytes);
          if (!closed) call.resume();
          return;
        }
        if (kind !== 0 || bytes.length > 49152) throw new Error();
      }
      const input = JSON.parse(bytes.toString('utf8'));
      if (!input || Array.isArray(input) || Object.keys(input).some(key =>
        !['agentID', 'sessionID', 'authorization', 'commandJSON'].includes(key)) ||
        typeof input.commandJSON !== 'string') throw new Error();
      const request = { agentID: input.agentID, sessionID: input.sessionID,
        authorization: { token: input.authorization, commandJSON: input.commandJSON },
        command: JSON.parse(input.commandJSON) };
      if (session) await session.renew(request);
      else {
        const opened = await service.videoBrowser(request, desktop);
        if (closed) { opened.close(); return; }
        session = opened;
        void pump();
      }
      if (!closed) call.resume();
    } catch { fail(); }
  });
}
