// Bidirectional host transport: fresh signed proofs in, encoded video out.
// Browser input continues on the independent, individually authorized RPC.
export function browserVideoRPC(service, call) {
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
        if (!call.write({ value: bytes })) await new Promise((resolve, reject) => {
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
      if (closed || !packet?.value || packet.value.length > 49152) throw new Error();
      const input = JSON.parse(Buffer.from(packet.value).toString('utf8'));
      if (!input || Array.isArray(input) || Object.keys(input).some(key =>
        !['agentID', 'sessionID', 'authorization', 'commandJSON'].includes(key)) ||
        typeof input.commandJSON !== 'string') throw new Error();
      const request = { agentID: input.agentID, sessionID: input.sessionID,
        authorization: { token: input.authorization, commandJSON: input.commandJSON },
        command: JSON.parse(input.commandJSON) };
      if (session) await session.renew(request);
      else {
        const opened = await service.videoBrowser(request);
        if (closed) { opened.close(); return; }
        session = opened;
        void pump();
      }
      if (!closed) call.resume();
    } catch { fail(); }
  });
}
