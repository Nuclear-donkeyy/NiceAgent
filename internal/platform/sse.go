package platform

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func WriteSSE(w http.ResponseWriter, event string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

