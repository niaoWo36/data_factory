package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"data_factory/internal/api"

	"github.com/gorilla/mux"
)

// Version and BuildTime are injected at build time via -ldflags.
var (
	Version   = "dev"
	BuildTime = "unknown"
)

//go:embed web
var webFS embed.FS

func main() {
	port       := flag.String("port", "8080", "HTTP server port")
	configFile := flag.String("config", "data_factory.json", "Path to persisted config file")
	version    := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *version {
		fmt.Printf("data_factory %s (built %s)\n", Version, BuildTime)
		return
	}

	srv, err := api.NewServer(*configFile)
	if err != nil {
		log.Fatalf("init server: %v", err)
	}
	defer srv.Cleanup()

	r := mux.NewRouter()

	// Register API routes.
	srv.RegisterRoutes(r)

	// Serve embedded static files.
	webSub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed sub: %v", err)
	}
	r.PathPrefix("/").Handler(http.FileServer(http.FS(webSub)))

	addr := ":" + *port
	log.Printf("data_factory %s listening on http://localhost%s", Version, addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
