const net = require("net");
const {frame, serveExtension} = require("./gebext");

async function handleCall(fn, args, kwargs) {
  if (fn === "add") return args[0] + args[1];
  if (fn === "greet") return "hello " + (kwargs.name || "world");
  if (fn === "echo") return args[0];
  throw new Error("unknown function: " + fn);
}

const host = process.env.EXT_HOST || "127.0.0.1";
const port = Number(process.env.EXT_PORT || "9104");

net.createServer(socket => {
  serveExtension(socket, "node_example", ["add", "echo", "greet"], handleCall).catch(error => {
    socket.write(frame(0, Buffer.from(JSON.stringify({id: 0, ok: false, error: error.message}))));
    socket.end();
  });
}).listen(port, host);
