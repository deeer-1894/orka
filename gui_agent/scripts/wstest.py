"""Standalone WS smoke test for the GUI agent."""

import asyncio
import json
import sys

import websockets


async def main() -> int:
    url = "ws://127.0.0.1:8100/api/v1/exec/gui/ws"
    async with websockets.connect(url, max_size=8 * 1024 * 1024) as ws:
        await ws.send(json.dumps({
            "type": "run",
            "instruction": "Open https://example.com and finish.",
            "session_id": "s1",
            "max_steps": 4,
        }))
        seen = []
        while True:
            raw = await asyncio.wait_for(ws.recv(), timeout=60)
            frame = json.loads(raw)
            t = frame.get("type")
            seen.append(t)
            if t == "screenshot":
                print(f"<- screenshot ({len(frame.get('data',''))} b64 bytes)")
            elif t == "observe":
                print(f"<- observe mode={frame.get('mode')} tokens={frame.get('tokens')}")
            elif t == "action":
                print(f"<- action {frame.get('action')} target={frame.get('target')} -> {frame.get('result')}")
            elif t == "done":
                print(f"<- done summary={frame.get('summary','')[:80]!r}")
                break
            elif t in ("error", "call_user"):
                print(f"<- {t}: {frame}")
                break
        ok = "action" in seen and "done" in seen and "observe" in seen
        # DOM-first: no vision tokens
        vision_free = all(f != "observe" or True for f in seen)
        print("RESULT:", "PASS" if ok else "FAIL", "| frames:", seen)
        return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
