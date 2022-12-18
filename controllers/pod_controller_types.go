package controllers

import (
	"time"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Log                         logr.Logger
	Scheme                      *runtime.Scheme
	ClientSet                   *kubernetes.Clientset
	SampleSize                  int
	EnableAnnotation            bool
	MinSecondsBetweenPodRestart int
}

type PodRequests struct {
	Name                 string
	ContainerRequests    []ContainerRequests
	Sample               int
	TimeSinceFirstSample int
	Timestamp            time.Time
}

type ContainerRequests struct {
	Name   string
	CPU    int64 // Nanocores
	Memory int64 // Mi
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
