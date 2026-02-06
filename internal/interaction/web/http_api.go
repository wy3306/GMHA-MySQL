package web

import (
	"fmt"
	"net/http"
)

func Run(port int) {
	mux := http.NewServeMux()

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Starting Web Server at %s...\n", addr)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Printf("Web server error: %v\n", err)
	}
}
