# SPDX-License-Identifier: BSD-2-Clause
# SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

"""The Python layer's demo, on the standard library alone.

The template's page on /, and ok on /healthz, each to GET or HEAD alone,
and an empty 404 to everything else: 8080 is an allowlist, as the Go core
keeps it, and /healthz sits on it beside the page, since an upstream
Python app has one port. It listens on every address, IPv6 and IPv4, or
on IPv4 alone where the kernel has no IPv6. As PID 1 it is sent SIGTERM to
stop, which nothing handles for it: it finishes what it is answering, and
exits 0.
"""

import http.server
import signal
import socket
import sys
import threading
from typing import override
from urllib.parse import urlsplit

# For an annotation alone, which nothing evaluates at run time
lazy from types import FrameType

PORT = 8080
PAGE = "Hello from oci-image-template"
ANSWERS = {"/": PAGE, "/healthz": "ok"}


class Handler(http.server.BaseHTTPRequestHandler):
    """Answers GET and HEAD of the two paths, and nothing else."""

    # The Server header names no version of Python
    server_version = "template"

    @override
    def version_string(self) -> str:
        return self.server_version

    def do_GET(self) -> None:
        """Send the path's answer."""
        self.answer(body=True)

    def do_HEAD(self) -> None:
        """Send the path's answer's headers."""
        self.answer(body=False)

    def answer(self, *, body: bool) -> None:
        """Send the answer the path has, or an empty 404, whatever the query."""
        text = ANSWERS.get(urlsplit(self.path).path)
        if text is None:
            self.refuse()
            return
        data = text.encode()
        self.send_response(http.HTTPStatus.OK)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        if body:
            self.wfile.write(data)

    def refuse(self) -> None:
        """Send an empty 404."""
        self.send_response(http.HTTPStatus.NOT_FOUND)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def __getattr__(self, name: str) -> object:
        """Refuse every other method with the same empty 404.

        http.server answers a method it has no do_ handler for with 501 and
        an HTML page.
        """
        if name.startswith("do_"):
            return self.refuse
        raise AttributeError(name)


class Server(http.server.ThreadingHTTPServer):
    """Listens on every address of both families, where IPv6 is there."""

    address_family = socket.AF_INET6

    @override
    def server_bind(self) -> None:
        # IPv4 too, on the same socket, whatever the kernel's default
        self.socket.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
        super().server_bind()


def listen() -> http.server.ThreadingHTTPServer:
    """Listen on 8080 on every address, or on every IPv4 one alone."""
    try:
        return Server(("::", PORT), Handler)
    except OSError:
        # No IPv6 in this kernel
        return http.server.ThreadingHTTPServer(("0.0.0.0", PORT), Handler)  # noqa: S104 - every address is the point


def main() -> int:
    """Serve until SIGTERM or SIGINT, then finish what is in flight."""
    server = listen()

    def stop(_signum: int, _frame: FrameType | None) -> None:
        # shutdown waits for serve_forever, which runs on this thread
        threading.Thread(target=server.shutdown).start()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    with server:
        server.serve_forever()
    return 0


if __name__ == "__main__":
    sys.exit(main())
