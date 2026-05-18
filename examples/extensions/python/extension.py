#!/usr/bin/env python3
import os

import gebext


def handle_call(fn, args, kwargs):
    if fn == "add":
        return args[0] + args[1]
    if fn == "greet":
        return "hello " + kwargs.get("name", "world")
    if fn == "echo":
        return args[0]
    raise ValueError("unknown function: " + fn)


def main():
    sock_path = os.environ.get("GEBLANG_EXT_SOCKET")
    if sock_path:
        try:
            os.unlink(sock_path)
        except FileNotFoundError:
            pass
        server = gebext.listen(sock_path=sock_path)
    else:
        host = os.environ.get("EXT_HOST", "127.0.0.1")
        port = int(os.environ.get("EXT_PORT", "9101"))
        server = gebext.listen(host=host, port=port)

    try:
        while True:
            conn, _ = server.accept()
            with conn:
                try:
                    gebext.serve_connection(conn, "python_example", ["add", "echo", "greet"], handle_call)
                except (BrokenPipeError, ConnectionResetError, EOFError, ValueError) as exc:
                    print(f"connection closed: {exc}", flush=True)
    finally:
        server.close()


if __name__ == "__main__":
    main()
