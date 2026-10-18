"""Server-owned effect classification; never trust a client's read-only flag.

Arbitrary evaluate/callFunctionOn remain mutations, even when their caller says
they are observing. Only a narrow Chromium context-loss acknowledgement proves
that an isolated-world command can no longer run in that world.
"""

READ_ONLY = frozenset({
    "Orka.observe",
    "Orka.getPageState", "Page.getFrameTree", "Page.captureScreenshot",
    "Page.createIsolatedWorld", "Page.enable", "Runtime.enable",
    "Runtime.releaseObject", "Runtime.releaseObjectGroup",
    "DOM.getDocument", "DOM.querySelector", "DOM.describeNode",
    "DOM.resolveNode", "DOM.getBoxModel",
})


def context_lost(error):
    text = str(error).lower()
    return any(marker in text for marker in (
        "cannot find context with specified id",
        "execution context was destroyed",
        "cannot find execution context",
        "cannot find object with given id",
    ))
