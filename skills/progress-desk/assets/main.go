// progress-desk: a local live tracker shared by a developer and a coding agent.
//
// state.json  - written by the development agent (progress, decisions, replies)
// inbox.jsonl - appended by this server when the developer sends from the page
//
// The page holds one WebSocket at /ws: the server pushes a full snapshot on
// connect and whenever either file changes; the page sends suggestions,
// decision answers and replies back over the same socket.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

var (
	dir       string
	addr      string
	mu        sync.Mutex // guards inbox appends, lastGood and subs
	subs      = map[chan []byte]struct{}{}
	lastGood  []byte
	validKind = map[string]bool{"suggestion": true, "decision": true, "reply": true}
)

func statePath() string  { return filepath.Join(dir, "state.json") }
func inboxPath() string  { return filepath.Join(dir, "inbox.jsonl") }
func reviewPath() string { return filepath.Join(dir, "review.json") }

// reviewVersion lets the page refetch review.json only when it changes.
func reviewVersion() int64 {
	if fi, err := os.Stat(reviewPath()); err == nil {
		return fi.ModTime().UnixNano()
	}
	return 0
}

// snapshot merges agent state and developer inbox into one payload.
// A half-written state.json is skipped and the last good copy is served.
// Caller holds mu.
func snapshot() []byte {
	st, err := os.ReadFile(statePath())
	if err != nil || !json.Valid(st) {
		return lastGood
	}
	inbox := []json.RawMessage{}
	if f, err := os.Open(inboxPath()); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if line := bytes.TrimSpace(sc.Bytes()); json.Valid(line) {
				inbox = append(inbox, append([]byte(nil), line...))
			}
		}
		f.Close()
	}
	lastGood, _ = json.Marshal(map[string]any{"type": "snapshot", "state": json.RawMessage(st), "inbox": inbox, "review_version": reviewVersion()})
	return lastGood
}

func broadcast() {
	mu.Lock()
	defer mu.Unlock()
	snap := snapshot()
	for ch := range subs {
		select {
		case ch <- snap:
		default: // slow client; it gets the next one
		}
	}
}

// watch polls file mtimes. ponytail: 1s polling, switch to fsnotify if latency matters.
func watch() {
	sig := func() string {
		s := ""
		for _, p := range []string{statePath(), inboxPath(), reviewPath()} {
			if fi, err := os.Stat(p); err == nil {
				s += fmt.Sprint(fi.ModTime().UnixNano(), fi.Size())
			}
		}
		return s
	}
	prev := sig()
	for range time.Tick(time.Second) {
		if cur := sig(); cur != prev {
			prev = cur
			broadcast()
		}
	}
}

// save appends one developer message to the inbox and returns the stored line.
func save(kind, ref, text string) ([]byte, error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 8000 || !validKind[kind] || len(ref) > 40 {
		return nil, fmt.Errorf("need kind (suggestion|decision|reply), text under 8000 chars, and a short ref")
	}
	now := time.Now()
	line, _ := json.Marshal(map[string]string{
		"id": fmt.Sprintf("s%d", now.UnixMilli()), "at": now.Format(time.RFC3339),
		"kind": kind, "ref": ref, "text": text,
	})
	mu.Lock()
	defer mu.Unlock()
	f, err := os.OpenFile(inboxPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return line, err
}

func serveWS(ws *websocket.Conn) {
	ch := make(chan []byte, 4)
	mu.Lock()
	subs[ch] = struct{}{}
	first := snapshot()
	mu.Unlock()
	defer func() { mu.Lock(); delete(subs, ch); mu.Unlock(); ws.Close() }()

	if websocket.Message.Send(ws, string(first)) != nil {
		return
	}

	// reader: developer -> inbox
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var in struct{ Kind, Ref, Text string }
			if err := websocket.JSON.Receive(ws, &in); err != nil {
				return
			}
			if _, err := save(in.Kind, in.Ref, in.Text); err != nil {
				msg, _ := json.Marshal(map[string]string{"type": "error", "message": err.Error()})
				ch <- msg
				continue
			}
			broadcast()
		}
	}()

	// writer: snapshots -> developer
	for {
		select {
		case <-done:
			return
		case msg := <-ch:
			if websocket.Message.Send(ws, string(msg)) != nil {
				return
			}
		}
	}
}

// sameOrigin refuses sockets opened by other sites in the same browser,
// so nothing but this page can write into the agent's inbox.
func sameOrigin(cfg *websocket.Config, r *http.Request) error {
	o, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || o.Host != r.Host {
		return fmt.Errorf("origin %q not allowed", r.Header.Get("Origin"))
	}
	cfg.Origin = o
	return nil
}

// loopbackOnly refuses any Host but 127.0.0.1/localhost, so a DNS-rebinding
// page (evil.com resolving to 127.0.0.1) can't read the page or pass the
// same-origin check with Origin == Host == evil.com.
func loopbackOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil || (host != "127.0.0.1" && host != "localhost") {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func routes() http.Handler {
	m := http.NewServeMux()
	m.Handle("/ws", websocket.Server{Handler: serveWS, Handshake: sameOrigin})
	m.HandleFunc("/review.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, reviewPath())
	})
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
	return loopbackOnly(m)
}

func main() {
	flag.StringVar(&addr, "addr", "127.0.0.1:8787", "listen address (keep it on 127.0.0.1)")
	flag.StringVar(&dir, "dir", ".", "directory holding index.html, state.json, inbox.jsonl, review.json")
	flag.Parse()
	go watch()
	log.Printf("progress-desk on http://%s (dir %s)", addr, dir)
	log.Fatal(http.ListenAndServe(addr, routes()))
}
