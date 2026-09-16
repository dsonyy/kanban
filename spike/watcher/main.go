package main

import (
	"fmt"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
)

func main() {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	if err := w.Add(os.Args[1]); err != nil {
		panic(err)
	}
	start := time.Now()
	for {
		select {
		case e := <-w.Events:
			fmt.Printf("%6dms %-8s %s\n", time.Since(start).Milliseconds(), e.Op, e.Name)
		case err := <-w.Errors:
			fmt.Println("ERR", err)
		}
	}
}
