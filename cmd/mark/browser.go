package main

import (
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gorilla/websocket"
)

// bowser serves the html output in the browser.
// client open an websocket connection, and the server push
// the new changes, once there's a new activity in the working file.
type browser struct {
	port      string
	file      string
	parseFunc func() string
	sync.RWMutex
	listeners []chan []byte
}

func (b *browser) watch() {
	watcher, err := fsnotify.NewWatcher()
	failOnErr(err, "create file watcher")
	failOnErr(watcher.Add(b.file), "watch file")
	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Write != fsnotify.Write {
				continue
			}
			buf := []byte(b.parseFunc())
			b.RLock()
			for _, l := range b.listeners {
				go func() {
					fmt.Println("new change")
					l <- buf
				}()
			}
			b.RUnlock()
			// listen for changes.
		}
	}
}

func (b *browser) Serve() {
	http.HandleFunc("/", b.page)
	http.HandleFunc("/ws", b.ws)
	failOnErr(http.ListenAndServe(net.JoinHostPort("localhost", b.port), nil), "create server")
}

// ws serves the websocket handler.
func (b *browser) ws(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		if _, ok := err.(websocket.HandshakeError); !ok {
			log.Println(err)
		}
		return
	}
	// register.
	b.Lock()
	l := make(chan []byte)
	b.listeners = append(b.listeners, l)
	b.Unlock()
	go b.write(ws, l)
	b.read(ws)
}

func (b *browser) read(ws *websocket.Conn) {
	defer ws.Close()
	// unregister.
	ws.SetReadLimit(512)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
	}
}

func (b *browser) write(ws *websocket.Conn, ch <-chan []byte) {
	pingTicker := time.NewTicker(pingPeriod)
	defer pingTicker.Stop()
	defer ws.Close()
	for {
		select {
		case buf := <-ch:
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.TextMessage, buf); err != nil {
				return
			}
		case <-pingTicker.C:
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := ws.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

// page serves the main page.
func (b *browser) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "Not found", 404)
		return
	}
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.Execute(w, struct {
		Data string
		Port string
	}{
		b.parseFunc(),
		b.port,
	})
}

var (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
	upgrader   = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
	page = template.Must(template.New("").Parse(`<!DOCTYPE html>
	<html lang="en">
		<head>
			<title>WebSocket Example</title>
		</head>
		<body>
			<pre id="fileData">{{.Data}}</pre>
			<script type="text/javascript">
				(function() {
					var data = document.getElementById("fileData");
					var conn = new WebSocket("ws://localhost:{{.Port}}/ws");
					conn.onclose = function(evt) {
						data.textContent = 'Connection closed';
					}
					conn.onmessage = function(evt) {
						console.log('file updated');
						data.textContent = evt.data;
					}
				})();
			</script>
		</body>
	</html>
	`))
)
