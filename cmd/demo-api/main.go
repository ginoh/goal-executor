package main

import (
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /message", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) })
	log.Fatal(http.ListenAndServe(":8080", mux))
}
