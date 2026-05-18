import json
import socket
import struct

FRAME_JSON = 0
FRAME_BINARY = 1


def write_frame(conn, frame_type, payload=b""):
    conn.sendall(struct.pack(">IB", len(payload), frame_type) + payload)


def read_frame(conn):
    header = conn.recv(5)
    if not header:
        return None, None
    while len(header) < 5:
        chunk = conn.recv(5 - len(header))
        if not chunk:
            raise EOFError("short frame header")
        header += chunk
    length, frame_type = struct.unpack(">IB", header)
    payload = b""
    while len(payload) < length:
        chunk = conn.recv(length - len(payload))
        if not chunk:
            raise EOFError("short frame payload")
        payload += chunk
    return frame_type, payload


def encode_value(value, slots):
    if isinstance(value, bytes):
        slot = len(slots)
        slots.append(value)
        return {"$type": "bytes", "slot": slot}
    return value


def decode_value(value, slots):
    if isinstance(value, dict) and value.get("$type") == "bytes":
        return slots[int(value["slot"])]
    return value


def serve_connection(conn, name, functions, handler):
    write_frame(conn, FRAME_JSON, json.dumps({
        "v": 1,
        "name": name,
        "functions": sorted(functions),
    }).encode())
    while True:
        frame_type, payload = read_frame(conn)
        if frame_type is None:
            return
        if frame_type != FRAME_JSON:
            continue
        req = json.loads(payload.decode())
        slots = []
        for _ in range(int(req.get("slots", 0))):
            slot_type, slot_payload = read_frame(conn)
            if slot_type != FRAME_BINARY:
                raise ValueError("expected binary slot")
            slots.append(slot_payload)
        if req.get("fn") == "__shutdown__":
            return
        try:
            args = [decode_value(arg, slots) for arg in req.get("args", [])]
            kwargs = {k: decode_value(v, slots) for k, v in req.get("kwargs", {}).items()}
            result = handler(req["fn"], args, kwargs)
            out_slots = []
            value = encode_value(result, out_slots)
            resp = {"id": req["id"], "ok": True, "value": value}
            if out_slots:
                resp["slots"] = len(out_slots)
            write_frame(conn, FRAME_JSON, json.dumps(resp).encode())
            for slot in out_slots:
                write_frame(conn, FRAME_BINARY, slot)
        except Exception as exc:
            write_frame(conn, FRAME_JSON, json.dumps({
                "id": req.get("id", 0),
                "ok": False,
                "error": str(exc),
            }).encode())


def listen(sock_path=None, host="127.0.0.1", port=9101):
    if sock_path:
        server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        server.bind(sock_path)
    else:
        server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        server.bind((host, port))
    server.listen(1)
    return server
