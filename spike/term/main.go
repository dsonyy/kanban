package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/exec"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

func main() {
	http.Handle("/", http.FileServer(http.Dir("static")))
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		cmd := exec.Command("tmux", "attach", "-t", r.URL.Query().Get("s"))
		cmd.Env = append(cmd.Environ(), "TERM=xterm-256color")
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
		if err != nil {
			log.Print(err)
			return
		}
		defer func() { f.Close(); cmd.Process.Kill(); cmd.Wait() }()
		ctx := r.Context()
		go func() {
			buf := make([]byte, 32*1024)
			for {
				n, err := f.Read(buf)
				if err != nil {
					c.Close(websocket.StatusNormalClosure, "")
					return
				}
				if c.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					return
				}
			}
		}()
		for {
			typ, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				var cols, rows uint16
				if _, err := fmt.Sscanf(string(data), "resize %d %d", &cols, &rows); err == nil {
					pty.Setsize(f, &pty.Winsize{Cols: cols, Rows: rows})
				}
				continue
			}
			f.Write(data)
		}
	})
	log.Fatal(http.ListenAndServe("127.0.0.1:7788", nil))
}
