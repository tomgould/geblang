package bytecode_test

import (
	"os"
	"testing"
)

// TestParityRedisTagOps pins the atomic Redis primitives the distributed cache-tag invalidation relies on - eval (Lua), incr, sadd, smembers, del - as byte-identical on both backends. Gated on GEBWEB_REDIS_TEST=host:port (a stateful native bridged to the engine on the VM).
func TestParityRedisTagOps(t *testing.T) {
	addr := os.Getenv("GEBWEB_REDIS_TEST")
	if addr == "" {
		t.Skip("set GEBWEB_REDIS_TEST=host:port to run")
	}
	src := `import io;
import redis;
let c = redis.connect("` + addr + `");
c.del("ptest:gen"); c.del("ptest:idx"); c.set("ptest:a", "1"); c.set("ptest:b", "1");
c.sadd("ptest:idx", "a"); c.sadd("ptest:idx", "b");
let lua = "redis.call('INCR', KEYS[1]) local m = redis.call('SMEMBERS', KEYS[2]) local n = 0 for _, x in ipairs(m) do n = n + redis.call('DEL', ARGV[1] .. x) end redis.call('DEL', KEYS[2]) return n";
let deleted = c.eval(lua, ["ptest:gen", "ptest:idx"], ["ptest:"]);
let a = c.get("ptest:a");
io.println("gen=" + (c.get("ptest:gen") as string) + " deleted=" + (deleted as string) + " a=${a}");
c.close();
`
	runParityWithStdlib(t, src, "gen=1 deleted=2 a=null\n")
}
