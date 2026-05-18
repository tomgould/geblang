package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

const (
	frameJSON   byte = 0
	frameBinary byte = 1
)

type extRequest struct {
	ID     int64          `json:"id"`
	Fn     string         `json:"fn"`
	Args   []any          `json:"args"`
	Kwargs map[string]any `json:"kwargs"`
	Slots  int            `json:"slots"`
}

type extHandler func(fn string, args []any, kwargs map[string]any, slots [][]byte) (any, [][]byte, error)

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	header := make([]byte, 5)
	binary.BigEndian.PutUint32(header[:4], uint32(len(payload)))
	header[4] = typ
	if err := writeAll(w, header); err != nil {
		return err
	}
	return writeAll(w, payload)
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func readFrame(r io.Reader) (byte, []byte, error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[:4])
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return header[4], payload, nil
}

func serveExtension(conn net.Conn, name string, functions []string, handler extHandler) {
	defer conn.Close()
	handshake, _ := json.Marshal(map[string]any{"v": 1, "name": name, "functions": functions})
	if err := writeFrame(conn, frameJSON, handshake); err != nil {
		return
	}
	for {
		typ, payload, err := readFrame(conn)
		if err != nil {
			return
		}
		if typ != frameJSON {
			continue
		}
		var req extRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			continue
		}
		slots := make([][]byte, req.Slots)
		for i := range slots {
			slotType, slot, err := readFrame(conn)
			if err != nil || slotType != frameBinary {
				return
			}
			slots[i] = slot
		}
		if req.Fn == "__shutdown__" {
			return
		}
		value, outSlots, callErr := handler(req.Fn, req.Args, req.Kwargs, slots)
		resp := map[string]any{"id": req.ID, "ok": callErr == nil, "value": value}
		if callErr != nil {
			resp["error"] = callErr.Error()
		}
		if len(outSlots) > 0 {
			resp["slots"] = len(outSlots)
		}
		data, _ := json.Marshal(resp)
		_ = writeFrame(conn, frameJSON, data)
		for _, slot := range outSlots {
			_ = writeFrame(conn, frameBinary, slot)
		}
	}
}

func bytesArg(value any, slots [][]byte) ([]byte, error) {
	marker, ok := value.(map[string]any)
	if !ok || marker["$type"] != "bytes" {
		return nil, fmt.Errorf("expected bytes marker")
	}
	slot := int(marker["slot"].(float64))
	if slot < 0 || slot >= len(slots) {
		return nil, fmt.Errorf("bytes slot out of range")
	}
	return slots[slot], nil
}
