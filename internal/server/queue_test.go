package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRoomCommandAdmissionNeverBlocksOnFullQueue(t *testing.T) {
	r := &Room{cmds: make(chan command, 1), done: make(chan struct{})}
	r.cmds <- command{kind: "blocker"}
	start := time.Now()
	if enqueueRoomCommand(r, command{kind: "op"}) {
		t.Fatal("full room queue accepted a command")
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("full room queue blocked command admission")
	}
	close(r.done)
	if enqueueRoomCommand(r, command{kind: "leave"}) {
		t.Fatal("closed room accepted a command")
	}
}

func TestReadPumpClosesBusyRoomWith1013WithoutParking(t *testing.T) {
	finished := make(chan struct{})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := upgrader.Upgrade(w, req, nil)
		if err != nil {
			return
		}
		r := &Room{cmds: make(chan command, 1), done: make(chan struct{}), limits: Limits{}.withDefaults()}
		r.cmds <- command{kind: "blocker"}
		c := &Client{room: r, send: make(chan []byte, 1)}
		go func() { c.readPump(conn); close(finished) }()
	}))
	defer httpServer.Close()
	url := "ws" + httpServer.URL[len("http"):]
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"ack"}`)); err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseTryAgainLater {
		t.Fatalf("close error=%v, want websocket 1013", err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("read pump remained blocked after queue saturation")
	}
}
