package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// TestDesk runs the real routes against a scratch dir and checks: the page
// loads, a same-origin socket gets a snapshot and can post, a foreign-origin
// socket and a non-loopback Host are refused.
func TestDesk(t *testing.T) {
	dir = t.TempDir()
	for _, f := range []string{"index.html", "state.json"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, f), b, 0o644)
	}
	srv := httptest.NewServer(routes()) // listens on 127.0.0.1
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	res, err := http.Get(srv.URL + "/")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("page: %v %v", err, res)
	}
	if b, _ := io.ReadAll(res.Body); !strings.Contains(string(b), "<title>") {
		t.Fatal("page: no <title>")
	}

	ws, err := websocket.Dial("ws://"+host+"/ws", "", srv.URL)
	if err != nil {
		t.Fatalf("same-origin dial: %v", err)
	}
	defer ws.Close()
	ws.SetDeadline(time.Now().Add(5 * time.Second))
	var snap struct {
		Type  string
		Inbox []map[string]string
	}
	if err := websocket.JSON.Receive(ws, &snap); err != nil || snap.Type != "snapshot" {
		t.Fatalf("first snapshot: %v %+v", err, snap)
	}
	websocket.JSON.Send(ws, map[string]string{"kind": "suggestion", "ref": "P1", "text": "check `this`"})
	if err := websocket.JSON.Receive(ws, &snap); err != nil || len(snap.Inbox) != 1 || snap.Inbox[0]["text"] != "check `this`" {
		t.Fatalf("snapshot after post: %v %+v", err, snap)
	}
	line, _ := os.ReadFile(inboxPath())
	var m map[string]string
	if json.Unmarshal(line, &m) != nil || m["kind"] != "suggestion" || m["id"] == "" {
		t.Fatalf("inbox.jsonl: %q", line)
	}

	if _, err := websocket.Dial("ws://"+host+"/ws", "", "http://evil.example"); err == nil {
		t.Fatal("foreign-origin socket was accepted")
	}

	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Host = "evil.example:" + strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusForbidden {
		t.Fatalf("non-loopback Host not refused: %v %v", err, res)
	}
}
