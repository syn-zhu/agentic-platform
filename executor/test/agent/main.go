// Test agent: a minimal HTTP server for validating the executor.
//
// Listens on $PORT (default 8080). Endpoints:
//   GET  /healthz    → 200 OK
//   POST /run        → SSE stream echoing the request body
//
// The SSE stream sends the payload back as a series of "data:" events,
// one per line, then closes. This is enough to validate the full
// executor → vsock → guest init → agent → SSE → vsock → waypoint flow.
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	http.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)

		// Echo the payload back as SSE events.
		lines := strings.Split(string(body), "\n")
		for _, line := range lines {
			fmt.Fprintf(w, "data: %s\n\n", line)
			if ok {
				flusher.Flush()
			}
		}

		// Final event.
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if ok {
			flusher.Flush()
		}
	})

	addr := ":" + port
	log.Printf("test agent listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
