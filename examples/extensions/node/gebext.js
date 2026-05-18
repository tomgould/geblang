const FRAME_JSON = 0;
const FRAME_BINARY = 1;

function frame(type, payload = Buffer.alloc(0)) {
  const header = Buffer.alloc(5);
  header.writeUInt32BE(payload.length, 0);
  header.writeUInt8(type, 4);
  return Buffer.concat([header, payload]);
}

function makeReader(socket) {
  let buffer = Buffer.alloc(0);
  const waiters = [];
  socket.on("data", chunk => {
    buffer = Buffer.concat([buffer, chunk]);
    while (waiters.length) waiters.shift()();
  });
  socket.on("close", () => {
    while (waiters.length) waiters.shift()();
  });
  return async function readFrame() {
    while (buffer.length < 5) {
      if (socket.destroyed) return null;
      await new Promise(resolve => waiters.push(resolve));
    }
    const length = buffer.readUInt32BE(0);
    const type = buffer.readUInt8(4);
    while (buffer.length < 5 + length) {
      if (socket.destroyed) return null;
      await new Promise(resolve => waiters.push(resolve));
    }
    const payload = buffer.subarray(5, 5 + length);
    buffer = buffer.subarray(5 + length);
    return {type, payload};
  };
}

function decode(value, slots) {
  if (value && value.$type === "bytes") return slots[value.slot];
  return value;
}

function encode(value, slots) {
  if (Buffer.isBuffer(value)) {
    const slot = slots.length;
    slots.push(value);
    return {$type: "bytes", slot};
  }
  return value;
}

async function serveExtension(socket, name, functions, handler) {
  socket.write(frame(FRAME_JSON, Buffer.from(JSON.stringify({
    v: 1,
    name,
    functions: [...functions].sort(),
  }))));
  const readFrame = makeReader(socket);
  while (!socket.destroyed) {
    const first = await readFrame();
    if (!first) return;
    if (first.type !== FRAME_JSON) continue;
    const req = JSON.parse(first.payload.toString("utf8"));
    const slots = [];
    for (let i = 0; i < (req.slots || 0); i++) {
      const slot = await readFrame();
      if (!slot || slot.type !== FRAME_BINARY) throw new Error("expected binary slot");
      slots.push(slot.payload);
    }
    if (req.fn === "__shutdown__") return socket.end();
    try {
      const args = (req.args || []).map(arg => decode(arg, slots));
      const kwargs = {};
      for (const [key, value] of Object.entries(req.kwargs || {})) {
        kwargs[key] = decode(value, slots);
      }
      const result = await handler(req.fn, args, kwargs);
      const outSlots = [];
      const value = encode(result, outSlots);
      const resp = {id: req.id, ok: true, value};
      if (outSlots.length) resp.slots = outSlots.length;
      socket.write(frame(FRAME_JSON, Buffer.from(JSON.stringify(resp))));
      for (const slot of outSlots) socket.write(frame(FRAME_BINARY, slot));
    } catch (error) {
      socket.write(frame(FRAME_JSON, Buffer.from(JSON.stringify({
        id: req.id || 0,
        ok: false,
        error: error.message,
      }))));
    }
  }
}

module.exports = {FRAME_JSON, FRAME_BINARY, frame, makeReader, decode, encode, serveExtension};
