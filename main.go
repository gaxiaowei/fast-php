package main

import (
	"log"
	"net/http"
)

type HttpHandle struct {
}

func (h HttpHandle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Hello World"))
}

func main() {
	log.Fatal(http.ListenAndServe(":8881", &HttpHandle{}))
}
