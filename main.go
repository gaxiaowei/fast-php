package main

import (
	"log"
	"net/http"
	"strings"
)

type HttpHandle struct {
}

func (h HttpHandle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for key, val := range r.Header {
		w.Write([]byte(key + ": "))
		w.Write([]byte(strings.Join(val, ",")))
		w.Write([]byte("\r\n"))
	}

	if r.Header.Get("Content-Type") {
		
	}
}

func main() {
	log.Fatal(http.ListenAndServe(":8881", &HttpHandle{}))
}
