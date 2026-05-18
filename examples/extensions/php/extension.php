<?php
require __DIR__ . '/gebext.php';

$host = getenv('EXT_HOST') ?: '127.0.0.1';
$port = getenv('EXT_PORT') ?: '9102';
$server = stream_socket_server("tcp://$host:$port", $errno, $errstr);
if (!$server) {
    fwrite(STDERR, "$errstr\n");
    exit(1);
}

$handler = function (string $fn, array $args, array $kwargs) {
    if ($fn === 'add') {
        return $args[0] + $args[1];
    }
    if ($fn === 'greet') {
        return 'hello ' . ($kwargs['name'] ?? 'world');
    }
    if ($fn === 'echo') {
        return 'bytes:' . $args[0];
    }
    throw new RuntimeException('unknown function: ' . $fn);
};

while ($conn = stream_socket_accept($server)) {
    try {
        serve_extension($conn, 'php_example', ['add', 'echo', 'greet'], $handler);
    } catch (Throwable $e) {
        fwrite(STDERR, "connection closed: " . $e->getMessage() . "\n");
    } finally {
        fclose($conn);
    }
}
