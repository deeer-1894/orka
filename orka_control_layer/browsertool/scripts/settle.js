(() => {
  // Isolated-world bookkeeping only; never patches page history or handlers.
  const key = '__orkaObservationStability';
  let state = globalThis[key];
  if (!state || state.document !== document) {
    state = {document, revision: 0};
    state.observer = new MutationObserver(() => { state.revision++; });
    state.observer.observe(document, {subtree: true, childList: true, attributes: true, characterData: true});
    globalThis[key] = state;
  }
  if (state.observer.takeRecords().length) state.revision++;
  return {revision: state.revision, ready: document.readyState !== 'loading'};
})()
