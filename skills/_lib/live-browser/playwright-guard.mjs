// playwright-core's Firefox network code throws when Firefox reports the end
// of a request that never had a response. A WebRTC TURN-over-TLS channel
// through the proxy does exactly that when the TURN server's certificate is
// refused: `FFNetworkManager._onRequestFinished` reads `setTransferSize` of
// a null response inside a protocol event handler, so the error is uncaught
// and would end the runtime with every browser in it. Only that error, thrown
// from that frame, is swallowed (and logged as one event); any other uncaught
// error still ends the process.

const MESSAGE = "Cannot read properties of null (reading 'setTransferSize')";
const FRAME = /^ {4}at FFNetworkManager\._onRequestFinished \(.*[\\/]playwright-core[\\/]lib[\\/]server[\\/]firefox[\\/]ffNetworkManager\.js:\d+:\d+\)$/;

export function isPlaywrightNullResponse(error) {
  if (!(error instanceof TypeError) || error.message !== MESSAGE || typeof error.stack !== 'string') return false;
  const lines = error.stack.split('\n');
  return lines[0] === `TypeError: ${MESSAGE}` && FRAME.test(lines[1] ?? '');
}

// Installs the guard on a process (once). `exit` ends the process for every
// other error, after writing it as Node would.
export function guardPlaywrightNullResponse({ target = process, log = console, exit = code => process.exit(code) } = {}) {
  const handler = error => {
    if (isPlaywrightNullResponse(error)) {
      log.warn(JSON.stringify({ event: 'live_browser_playwright_null_response' }));
      return;
    }
    log.error(error);
    exit(1);
  };
  target.on('uncaughtException', handler);
  return () => target.off('uncaughtException', handler);
}
