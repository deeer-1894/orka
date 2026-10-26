"""Entrypoint for the GUI executor service."""

from __future__ import annotations

import os

import uvicorn


def main() -> None:
    # Loopback unless told otherwise: the container sets 0.0.0.0 explicitly
    # and publishes the port on the host's loopback only.
    host = os.getenv("GUI_HOST", "127.0.0.1")
    port = int(os.getenv("GUI_PORT", "8100"))
    uvicorn.run("service.web_socket.server:app", host=host, port=port, log_level="info")


if __name__ == "__main__":
    main()
