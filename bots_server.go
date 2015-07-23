package main

import "log"
import "net/http"
import "golang.org/x/net/websocket"
import "github.com/dantoye/bots/game"

func main() {
	log.Println("Starting Webserver")
	http.Handle("/ws", websocket.Handler(game.ClientConnected))
	http.Handle("/", http.FileServer(http.Dir("public")))
	http.ListenAndServe(":8080", nil)
}
