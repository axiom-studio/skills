// main() owns resource cleanup. Closing IPC afterwards lets Node exit even
// when a speech/grant listener was registered on the child process.
export async function runWorker(main, processRef = process, report = console.error) {
  try {
    await main();
  } catch {
    report('Meeting worker failed; see the session status in chat.');
    processRef.exitCode = 1;
  } finally {
    if (processRef.connected) processRef.disconnect();
  }
}
