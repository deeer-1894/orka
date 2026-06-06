"""Entrypoint for the GUI executor service."""

from __future__ import annotations

import os

import uvicorn


def main() -> None:
    host = os.getenv("GUI_HOST", "0.0.0.0")
    port = int(os.getenv("GUI_PORT", "8100"))
    uvicorn.run("service.web_socket.server:app", host=host, port=port, log_level="info")


if __name__ == "__main__":
    main()
