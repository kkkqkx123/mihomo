package route

import (
	"os"

	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/log"

	"github.com/metacubex/chi"
	"github.com/metacubex/chi/render"
	"github.com/metacubex/http"
)

func shutdownRouter() http.Handler {
	r := chi.NewRouter()
	r.Post("/", shutdown)
	return r
}

func shutdown(w http.ResponseWriter, r *http.Request) {
	render.JSON(w, r, render.M{"status": "ok"})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {
		log.Infoln("shutting down via API request")
		executor.Shutdown()
		os.Exit(0)
	}()
}
