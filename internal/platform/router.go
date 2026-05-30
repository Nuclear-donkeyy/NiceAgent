package platform

import "net/http"

func Method(method string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handler(w, r)
	}
}

