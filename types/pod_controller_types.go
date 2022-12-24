package types

import (
	"time"

	v1 "k8s.io/api/core/v1"
)

type PodRequests struct {
	Name              string // Name of Deployment Controller
	Namespace         string
	ContainerRequests []ContainerRequests
	Sample            int
	// TimeSinceFirstSample float64
	// Timestamp            time.Time
}

type ContainerRequests struct {
	Name      string
	CPU       int64 // Nanocores
	MaxCPU    int64
	MinCPU    int64
	Memory    int64 // Mi
	MaxMemory int64
	MinMemory int64
}

type NewContainerRequests struct {
	Name     string
	Requests v1.ResourceRequirements
}

type PodMetricsRestData struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
	Metadata   struct {
		Name              string    `json:"name"`
		Namespace         string    `json:"namespace"`
		CreationTimestamp time.Time `json:"creationTimestamp"`
		Labels            struct {
			App                             string `json:"app"`
			PodTemplateHash                 string `json:"pod-template-hash"`
			Release                         string `json:"release"`
			SecurityIstioIoTLSMode          string `json:"security.istio.io/tlsMode"`
			ServiceIstioIoCanonicalName     string `json:"service.istio.io/canonical-name"`
			ServiceIstioIoCanonicalRevision string `json:"service.istio.io/canonical-revision"`
		} `json:"labels"`
	} `json:"metadata"`
	Timestamp  time.Time `json:"timestamp"`
	Window     string    `json:"window"`
	Containers []struct {
		Name  string `json:"name"`
		Usage struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		} `json:"usage"`
	} `json:"containers"`
}
