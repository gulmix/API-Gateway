package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	name := os.Getenv("NAME")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello from %s, path %s\n", name, r.URL.Path)
	})

	http.ListenAndServe(":"+port, nil)
}
