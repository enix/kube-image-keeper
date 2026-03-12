package controller

import (
	"encoding/json"
	"net/http"
	"time"

	kuikv1alpha1 "github.com/enix/kube-image-keeper/api/kuik/v1alpha1"
	"github.com/enix/kube-image-keeper/internal"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type StatusHandler struct {
	Client client.Client
}

type statusImage struct {
	CISA        string  `json:"cisa,omitempty"`
	Image       string  `json:"image"`
	Registry    string  `json:"registry,omitempty"`
	Status      string  `json:"status"`
	UnusedSince *string `json:"unusedSince"`
	LastError   string  `json:"lastError,omitempty"`
	LastMonitor *string `json:"lastMonitor"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	groupBy := r.URL.Query().Get("groupBy")
	if groupBy != "" {
		if groupBy != "cisa" && groupBy != "registry" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(errorResponse{
				Error: "groupBy must be \"cisa\" or \"registry\"",
			})
			return
		}
	}

	cisaList := &kuikv1alpha1.ClusterImageSetAvailabilityList{}
	if err := h.Client.List(r.Context(), cisaList); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(errorResponse{
			Error: "failed to list ClusterImageSetAvailability resources: " + err.Error(),
		})
		return
	}

	groups := map[string][]statusImage{}
	items := []statusImage{}
	total := 0

	for _, cisa := range cisaList.Items {
		for _, image := range cisa.Status.Images {
			registry, _, err := internal.RegistryAndPathFromReference(image.Image)
			if err != nil {
				continue
			}

			si := statusImage{
				Image:     image.Image,
				Status:    string(image.Status),
				LastError: image.LastError,
			}

			if image.UnusedSince != nil && !image.UnusedSince.IsZero() {
				t := image.UnusedSince.Time.UTC().Format(time.RFC3339)
				si.UnusedSince = &t
			}
			if image.LastMonitor != nil && !image.LastMonitor.IsZero() {
				t := image.LastMonitor.Time.UTC().Format(time.RFC3339)
				si.LastMonitor = &t
			}

			si.CISA = cisa.Name
			si.Registry = registry

			if groupBy != "" {
				var key string
				switch groupBy {
				case "cisa":
					si.CISA = ""
					key = cisa.Name
				case "registry":
					si.Registry = ""
					key = registry
				}

				groups[key] = append(groups[key], si)
			} else {
				items = append(items, si)
			}

			total++
		}
	}

	if groupBy != "" {
		json.NewEncoder(w).Encode(map[string]any{
			"groupBy": groupBy,
			"groups":  groups,
			"total":   total,
		})
	} else {
		json.NewEncoder(w).Encode(map[string]any{
			"items": items,
			"total": total,
		})
	}
}
