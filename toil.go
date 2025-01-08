package main

import "io"
import "time"
import "bytes"
import "runtime"
import "net/http"
import "path/filepath"

import "os"
import "os/exec"

import "github.com/gorilla/websocket"

/*
	@todo

	# Root domain replacement

	An optional argument to pass a root domain and find/replace
	it inside the html file content, so you can develop with
	absolute paths. Not a high priority, because my static site
	builder (lichendust/spindle) solves this problem for me.

	# Get rid of gorilla/websocket

	It's supposedly possible to do this without using a middleware
	library, just with net/http.  I haven't tried it.  I might do.
*/

const TOIL = "Toil v0.1.4"

const SERVE_PORT     = ":3456"
const RELOAD_PREFIX  = "/_toil/"
const RELOAD_ADDRESS = RELOAD_PREFIX + "reload"

const TIME_WRITE_WAIT  = 10 * time.Second
const TIME_PONG_WAIT   = 60 * time.Second
const TIME_PING_PERIOD = (TIME_PONG_WAIT * 9) / 10

func main() {
	args := os.Args[1:]

	if len(args) > 0 {
		switch args[0] {
		case "help", "usage":
			println(TOIL)
			println("toil [optional-path]")
			return
		default:
			os.Chdir(args[0])
		}
	}

	the_server := http.NewServeMux()

	the_hub := new(Client_Hub)

	the_hub.clients    = make(map[*Client]bool, 4)
	the_hub.broadcast  = make(chan []byte)
	the_hub.register   = make(chan *Client)
	the_hub.unregister = make(chan *Client)

	go the_hub.run()

	// server components
	the_server.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Cache-Control", "no-cache")

		incoming_path := r.URL.Path

		if incoming_path == "/" {
			incoming_path = "index"
		}
		if incoming_path[0] == '/' {
			incoming_path = incoming_path[1:]
		}

		if filepath.Ext(incoming_path) == "" {
			does_exist, is_dir := exists(incoming_path)

			if does_exist && is_dir {
				index_path := filepath.ToSlash(filepath.Join(incoming_path, "index.html"))
				index_exists, _ := exists(index_path)

				if index_exists {
					serve_file(w, index_path)
					return
				}
			}

			incoming_path += ".html"
			does_exist, _ = exists(incoming_path)
			if does_exist {
				serve_file(w, incoming_path)
				return
			}

			w.WriteHeader(http.StatusNotFound)
			return
		}

		http.ServeFile(w, r, incoming_path)
	})

	// socket reloader
	the_server.HandleFunc(RELOAD_ADDRESS, func(w http.ResponseWriter, r *http.Request) {
		register_client(the_hub, w, r)
	})

	// start server
	go func() {
		http.ListenAndServe(SERVE_PORT, the_server)
	}()

	open_browser(SERVE_PORT)

	// monitor files for changes
	last_run := time.Now()

	for range time.Tick(time.Second) {
		first := false

		filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}

			// this is here so we skip the . folder itself
			if !first { first = true; return nil }

			if info.ModTime().After(last_run) {
				send_reload(the_hub)
				last_run = time.Now()

				// random error that Walk would never return to exit early
				// why they don't just provide one like SkipDir is beyond me
				return io.EOF
			}

			return nil
		})
	}
}

func open_browser(port string) {
	url := "http://localhost" + port

	var err error

	switch runtime.GOOS {
	case "windows": err = exec.Command("explorer", url).Start()
	case "darwin":  err = exec.Command("open",     url).Start()
	case "linux":   err = exec.Command("xdg-open", url).Start()
	}

	if err != nil {
		eprintln("failed to open browser automatically")
	}

	println(TOIL)
	println(url)
}

func serve_file(w http.ResponseWriter, file_name string) {
	file_bytes := bytes.Replace(load_file(file_name), []byte("</head>"), []byte(RELOAD_SCRIPT), 1)
	w.Write([]byte(file_bytes))
}

func exists(file string) (bool, bool) {
	f, err := os.Stat(file)
	if err != nil {
		return false, false
	}
	return true, f.IsDir()
}

func load_file(source_file string) []byte {
	content, err := os.ReadFile(source_file)
	if err != nil {
		return nil
	}
	return content
}

func send_reload(the_hub *Client_Hub) {
	the_hub.broadcast <- []byte("reload")
}

const RELOAD_SCRIPT = `<script type='text/javascript'>function fresh_reload() {
	var socket = new WebSocket("ws://" + window.location.host + "` + RELOAD_ADDRESS + `");
	socket.onclose = function(evt) {
		setTimeout(() => fresh_reload(), 2000);
	};
	socket.onmessage = function(evt) {
		location.reload();
	};
};
fresh_reload()</script></head>`

type Client_Hub struct {
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
}

func (h *Client_Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true

		case client := <-h.unregister:
			if ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}

		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

type Client struct {
	socket  *websocket.Conn
	send    chan []byte
}

func (c *Client) read_pump(the_hub *Client_Hub) {
	defer func() {
		the_hub.unregister <- c
		c.socket.Close()
	}()

	for {
		_, _, err := c.socket.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (c *Client) write_pump() {
	ticker := time.NewTicker(TIME_PING_PERIOD)
	defer func() {
		ticker.Stop()
		c.socket.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.write(websocket.CloseMessage, []byte{})
				return
			}

			c.socket.SetWriteDeadline(time.Now().Add(TIME_WRITE_WAIT))

			w, err := c.socket.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}

			w.Write(message)

			n := len(c.send)

			for i := 0; i < n; i += 1 {
				w.Write([]byte{'\n'})
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			if err := c.write(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

func (c *Client) write(mt int, payload []byte) error {
	c.socket.SetWriteDeadline(time.Now().Add(TIME_WRITE_WAIT))
	return c.socket.WriteMessage(mt, payload)
}

func register_client(the_hub *Client_Hub, w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		eprintln("failed to register new client")
	}

	the_client := &Client{
		socket: conn,
		send:   make(chan []byte, 256),
	}

	the_hub.register <- the_client

	go the_client.write_pump()
	the_client.read_pump(the_hub)
}

func println(phrases ...string) {
	l := len(phrases) - 1
	for i, w := range phrases {
		os.Stdout.WriteString(w)
		if i < l {
			os.Stdout.WriteString(" ")
		}
	}
	os.Stdout.WriteString("\n")
}

func eprintln(phrases ...string) {
	l := len(phrases) - 1
	for i, w := range phrases {
		os.Stderr.WriteString(w)
		if i < l {
			os.Stderr.WriteString(" ")
		}
	}
	os.Stderr.WriteString("\n")
}
