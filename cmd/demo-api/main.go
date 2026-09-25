// demo-api queries PostgreSQL through the psql client shipped in its image.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"
)

func main() {
	version, host := os.Getenv("API_VERSION"), os.Getenv("DB_HOST")
	if (version != "v1" && version != "v2") || host == "" {
		log.Fatal("API_VERSION v1/v2 and DB_HOST are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /data", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "psql", "-X", "-h", host, "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-Atqc", `SELECT json_build_object('dataset',dataset,'generation',generation,'value',value)::text FROM goal_seed WHERE id=1;`)
		out, err := cmd.Output()
		if err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		var data map[string]string
		if json.Unmarshal(out, &data) != nil || data["generation"] == "" {
			http.Error(w, "dataset unavailable", http.StatusServiceUnavailable)
			return
		}
		data["version"] = version
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(data)
	})
	server := http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	log.Fatal(server.ListenAndServe())
}
