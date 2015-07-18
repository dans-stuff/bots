package main

import "log"
import "net/http"

func main() {
	log.Println("Starting Webserver")
	http.Handle("/", http.FileServer(http.Dir("public")))
	http.ListenAndServe(":8080", nil)
}
