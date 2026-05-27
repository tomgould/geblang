package evaluator

import (
	"io"
	"strings"
	"testing"

	gruntime "geblang/internal/runtime"

	gorillawebsocket "github.com/gorilla/websocket"
)

func TestHTTPHandlerUpgradesWebSocket(t *testing.T) {
	e := New(io.Discard)
	handler := gruntime.Function{Native: func(this *gruntime.Instance, args []gruntime.Value) (gruntime.Value, error) {
		wsHandler := gruntime.Function{Native: func(this *gruntime.Instance, args []gruntime.Value) (gruntime.Value, error) {
			conn, err := e.websocketConn(args[0])
			if err != nil {
				return nil, err
			}
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				return nil, err
			}
			if err := conn.WriteMessage(messageType, []byte("echo:"+string(data))); err != nil {
				return nil, err
			}
			return gruntime.Null{}, nil
		}}
		entries := map[string]gruntime.DictEntry{}
		putDict(entries, "websocket", wsHandler)
		return gruntime.Dict{Entries: entries}, nil
	}}

	server := newLocalHTTPTestServer(t, e.httpHandler(handler, nil))
	defer server.Close()

	conn, _, err := gorillawebsocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(gorillawebsocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("write websocket message: %v", err)
	}
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}
	if messageType != gorillawebsocket.TextMessage {
		t.Fatalf("message type: got %d, want %d", messageType, gorillawebsocket.TextMessage)
	}
	if string(data) != "echo:hello" {
		t.Fatalf("message: got %q, want %q", string(data), "echo:hello")
	}
}
