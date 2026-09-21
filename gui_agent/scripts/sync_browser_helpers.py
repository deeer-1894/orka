"""Package canonical Go-owned helpers for the fixed private browser dispatcher.

Run from a repository checkout before building the GUI image. This is generation,
not a second editable implementation; the Go bundle contract checks for drift.
"""
import json
from pathlib import Path

root = Path(__file__).resolve().parents[2]
source = root / "orka_control_layer/browsertool/scripts"
target = root / "gui_agent/service/browser_helpers.json"
names = ("dom", "observation", "snapshot", "settle")
target.write_text(json.dumps({name: (source / (name + ".js")).read_text() for name in names}, ensure_ascii=False, indent=2) + "\n")
