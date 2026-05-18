<?php
const FRAME_JSON = 0;
const FRAME_BINARY = 1;

function write_frame($conn, int $type, string $payload = ''): void {
    write_all($conn, pack('NC', strlen($payload), $type) . $payload);
}

function write_all($conn, string $data): void {
    $offset = 0;
    $length = strlen($data);
    while ($offset < $length) {
        $written = fwrite($conn, substr($data, $offset));
        if ($written === false || $written === 0) {
            throw new RuntimeException('short write');
        }
        $offset += $written;
    }
}

function read_exact($conn, int $length): string {
    $data = '';
    while (strlen($data) < $length) {
        $chunk = fread($conn, $length - strlen($data));
        if ($chunk === '' || $chunk === false) {
            throw new RuntimeException('short frame');
        }
        $data .= $chunk;
    }
    return $data;
}

function read_frame($conn): ?array {
    $header = fread($conn, 5);
    if ($header === '' || $header === false) {
        return null;
    }
    while (strlen($header) < 5) {
        $header .= read_exact($conn, 5 - strlen($header));
    }
    $parts = unpack('Nlength/Ctype', $header);
    return [$parts['type'], read_exact($conn, $parts['length'])];
}

function decode_value($value, array $slots) {
    if (is_array($value) && ($value['$type'] ?? null) === 'bytes') {
        return $slots[(int)$value['slot']];
    }
    return $value;
}

function encode_value($value, array &$slots) {
    if (is_string($value) && str_starts_with($value, "bytes:")) {
        $slot = count($slots);
        $slots[] = substr($value, 6);
        return ['$type' => 'bytes', 'slot' => $slot];
    }
    return $value;
}

function serve_extension($conn, string $name, array $functions, callable $handler): void {
    sort($functions);
    write_frame($conn, FRAME_JSON, json_encode(['v' => 1, 'name' => $name, 'functions' => $functions]));
    while (true) {
        $frame = read_frame($conn);
        if ($frame === null) {
            return;
        }
        [$type, $payload] = $frame;
        if ($type !== FRAME_JSON) {
            continue;
        }
        $req = json_decode($payload, true);
        $slots = [];
        for ($i = 0; $i < (int)($req['slots'] ?? 0); $i++) {
            [$slotType, $slotPayload] = read_frame($conn);
            if ($slotType !== FRAME_BINARY) {
                throw new RuntimeException('expected binary slot');
            }
            $slots[] = $slotPayload;
        }
        if (($req['fn'] ?? '') === '__shutdown__') {
            return;
        }
        try {
            $args = array_map(fn($v) => decode_value($v, $slots), $req['args'] ?? []);
            $kwargs = [];
            foreach (($req['kwargs'] ?? []) as $key => $value) {
                $kwargs[$key] = decode_value($value, $slots);
            }
            $result = $handler($req['fn'], $args, $kwargs);
            $outSlots = [];
            $value = encode_value($result, $outSlots);
            $resp = ['id' => $req['id'], 'ok' => true, 'value' => $value];
            if ($outSlots) {
                $resp['slots'] = count($outSlots);
            }
            write_frame($conn, FRAME_JSON, json_encode($resp));
            foreach ($outSlots as $slot) {
                write_frame($conn, FRAME_BINARY, $slot);
            }
        } catch (Throwable $e) {
            write_frame($conn, FRAME_JSON, json_encode(['id' => $req['id'] ?? 0, 'ok' => false, 'error' => $e->getMessage()]));
        }
    }
}
