/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-logr/logr"
	"github.com/labstack/gommon/log"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	ClientSet   *kubernetes.Clientset
	SampleSize  int
	EnableLabel bool
}

type PodRequests struct {
	Name              string
	ContainerRequests []ContainerRequests
	Sample            int
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

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch

const (
	addPodNameLabelAnnotation = "auto.request.operator/optimize"
)

// cacheKeyFunc defines the key function required in TTLStore.
const cacheTTL = 60 * time.Second

func cacheKeyFunc(obj interface{}) (string, error) {
	return obj.(PodRequests).Name, nil
}

var cacheStore = cache.NewTTLStore(cacheKeyFunc, cacheTTL)

// Reconcile handles a reconciliation request for a Pod.
// If the Pod has the addPodNameLabelAnnotation annotation, then Reconcile
// will make sure the podNameLabel label is present with the correct value.
// If the annotation is absent, then Reconcile will make sure the label is too.
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("pod", req.NamespacedName)

	/*
		Step 0: Fetch the Pod from the Kubernetes API.
	*/

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			// we'll ignore not-found errors, since they can't be fixed by an immediate
			// requeue (we'll need to wait for a new notification), and we can get them
			// on deleted requests.
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch Pod")
		return ctrl.Result{}, err
	}

	/*
	   Step 1: Add or remove the label.
	*/

	addPodNameLabelAnnotation := pod.Annotations[addPodNameLabelAnnotation] == "true"
	// labelIsPresent := pod.Labels[podNameLabel] == pod.Name

	// if labelShouldBePresent == labelIsPresent {
	// 	// The desired state and actual state of the Pod are the same.
	// 	// No further action is required by the operator at this moment.
	// 	log.Info("no update required")
	// 	return ctrl.Result{}, nil
	// }
	// create the config object from kubeconfig
	// https://github.com/kubernetes/client-go/tree/master/examples#configuration

	// {"kind":"PodMetrics","apiVersion":"metrics.k8s.io/v1beta1","metadata":{"name":"swagger-swaggerui-54c4559467-xgs62","namespace":"automation-namespace","creationTimestamp":"2022-12-14T14:14:31Z","labels":{"app":"swaggerui","pod-template-hash":"54c4559467","release":"swagger","security.istio.io/tlsMode":"istio","service.istio.io/canonical-name":"swaggerui","service.istio.io/canonical-revision":"latest"}},"timestamp":"2022-12-14T14:14:10Z","window":"13s","containers":[{"name":"istio-proxy","usage":{"cpu":"31576563n","memory":"198012Ki"}},{"name":"swaggerui","usage":{"cpu":"20527n","memory":"9136Ki"}}]}
	if (!r.EnableLabel) || (r.EnableLabel && addPodNameLabelAnnotation) {
		log.Info("Checking Pod: " + pod.Name + " in namespace " + pod.Namespace)

		// go func() {
		data, err := r.ClientSet.RESTClient().Get().AbsPath(fmt.Sprintf("apis/metrics.k8s.io/v1beta1/namespaces/%v/pods/%v", pod.Namespace, pod.Name)).DoRaw(ctx)

		if err != nil {
			log.Error(err, "failed to get stats from pod")
			return ctrl.Result{}, err
		}
		PodUsageData := GeneratePodRequestsObjectFromRestData(data)
		// PodRequestsData := GetPodRequests(pod)

		SumPodRequest := PodRequests{Name: pod.Name, ContainerRequests: []ContainerRequests{}}
		// for _, r := range PodRequestsData.ContainerRequests {
		// temp := r
		// for _, v := range PodUsageData.ContainerRequests {
		// 	if v.Name == r.Name {
		// 		temp.CPU = r.CPU - v.CPU
		// 		temp.Memory = r.Memory - v.Memory
		// 	}
		// }
		// if temp.CPU < 0 {
		// 	temp.CPU = 0
		// }
		// if temp.Memory < 0 {
		// 	temp.Memory = 0
		// }
		SumPodRequest.ContainerRequests = PodUsageData.ContainerRequests
		// }

		LatestPodRequest, err := fetchFromCache(cacheStore, pod.Name)
		if err != nil {
			// log.Error(err, err.Error())
			SumPodRequest.Sample = 0
			// log.Info("Adding to cache sample: 0")
			addToCache(cacheStore, SumPodRequest)
		} else {
			// log.Info("Adding to cache sample: " + fmt.Sprint((LatestPodRequest.Sample + 1)))
			if err != nil {
				log.Error(err, err.Error())
			} else {
				SumPodRequest.Sample = LatestPodRequest.Sample + 1
				for _, sumC := range SumPodRequest.ContainerRequests {
					for _, latestC := range LatestPodRequest.ContainerRequests {
						if latestC.Name == sumC.Name {
							sumCAddr := &sumC
							sumCAddr.CPU += latestC.CPU
							sumCAddr.Memory += latestC.Memory
						}
					}
				}
				err := deleteFromCache(cacheStore, LatestPodRequest)
				if err != nil {
					log.Error(err, err.Error())
				}
				err = addToCache(cacheStore, SumPodRequest)
				if err != nil {
					log.Error(err, err.Error())
				}
			}
		}
		// log.Info(fmt.Sprint(SumPodRequest))

		if SumPodRequest.Sample == r.SampleSize {
			PodChange := false
			Requests := []NewContainerRequests{}
			log.Info(fmt.Sprint(SumPodRequest))
			log.Info("Pod Being Checked")
			for _, c := range SumPodRequest.ContainerRequests {
				AverageUsageCPU := c.CPU / int64(SumPodRequest.Sample)
				AverageUsageMemory := c.Memory / int64(SumPodRequest.Sample)
				PodRequestsData := GetPodRequests(pod)
				for _, currentC := range PodRequestsData.ContainerRequests {
					if currentC.Name == c.Name {
						for i, v := range pod.Spec.Containers {
							if v.Name == c.Name {
								log.Info(c.Name)
								log.Info(fmt.Sprint("Comparing CPU: ", fmt.Sprintf("%dm", AverageUsageCPU), " < ", fmt.Sprintf("%dm", currentC.CPU)))
								log.Info(fmt.Sprint("Comparing Memory: ", fmt.Sprintf("%dMi", AverageUsageMemory), " < ", fmt.Sprintf("%dMi", currentC.Memory)))
								if AverageUsageCPU < currentC.CPU {
									if AverageUsageCPU > 0 {
										pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", AverageUsageCPU))
										PodChange = true
									}
								}
								if AverageUsageMemory < currentC.Memory {
									if AverageUsageMemory > 0 {
										pod.Spec.Containers[i].Resources.Requests[v1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%dMi", AverageUsageMemory))
										PodChange = true
									}
								}
								Requests = append(Requests, NewContainerRequests{Name: c.Name, Requests: pod.Spec.Containers[i].Resources})
							}
						}
					}
				}
			}
			if PodChange {
				pod.Annotations["auto.request.operator/changed"] = "true"
				log.Info("Pod Requests Will Change")

				if len(pod.OwnerReferences) == 0 {
					log.Info("Pod has no owner")
					return r.UpdatePod(&pod, ctx)
				}

				var ownerName string

				switch pod.OwnerReferences[0].Kind {
				case "ReplicaSet":
					log.Info("Is Owned by Deployment")
					replica, err := r.ClientSet.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, pod.OwnerReferences[0].Name, metav1.GetOptions{})
					if err != nil {
						log.Error(err, err.Error())
					}

					ownerName = replica.OwnerReferences[0].Name
					// ownerKind = "Deployment"
					if replica.OwnerReferences[0].Kind == "Deployment" {
						deployment, err := r.ClientSet.AppsV1().Deployments(pod.Namespace).Get(ctx, ownerName, metav1.GetOptions{})
						if err != nil {
							log.Error(err, err.Error())
						}
						for _, podContainer := range Requests {
							for i, depContainer := range deployment.Spec.Template.Spec.Containers {
								if depContainer.Name == podContainer.Name {
									if deployment.Spec.Template.Spec.Containers[i].Resources.Requests != nil {
										log.Info(fmt.Sprint("Setting", podContainer.Requests))
										deployment.Spec.Template.Spec.Containers[i].Resources = podContainer.Requests
									}
								}
							}
						}
						deployment.Annotations["auto.request.operator/changed"] = "true"
						//deployment.Spec.Template.Annotations["auto.request.operator/changed"] = "true"
						r.UpdatePod(deployment, ctx)
					}
				case "DaemonSet":
					log.Info("Is Owned by DaemonSet")
					ownerName = pod.OwnerReferences[0].Name
					// ownerKind = pod.OwnerReferences[0].Kind

					deployment, err := r.ClientSet.AppsV1().DaemonSets(pod.Namespace).Get(ctx, ownerName, metav1.GetOptions{})
					if err != nil {
						log.Error(err, err.Error())
					}
					for _, podContainer := range Requests {
						for i, depContainer := range deployment.Spec.Template.Spec.Containers {
							if depContainer.Name == podContainer.Name {
								log.Info(fmt.Sprint("Setting", podContainer.Requests))
								deployment.Spec.Template.Spec.Containers[i].Resources = podContainer.Requests
							}
						}
					}
					deployment.Annotations["auto.request.operator/changed"] = "true"
					//deployment.Spec.Template.Annotations["auto.request.operator/changed"] = "true"
					r.UpdatePod(deployment, ctx)
				case "StatefulSet":
					log.Info("Is Owned by StatefulSet")
					ownerName = pod.OwnerReferences[0].Name
					// ownerKind = pod.OwnerReferences[0].Kind

					deployment, err := r.ClientSet.AppsV1().StatefulSets(pod.Namespace).Get(ctx, ownerName, metav1.GetOptions{})
					if err != nil {
						log.Error(err, err.Error())
					}
					for _, podContainer := range Requests {
						for i, depContainer := range deployment.Spec.Template.Spec.Containers {
							if depContainer.Name == podContainer.Name {
								log.Info(fmt.Sprint("Setting", podContainer.Requests))
								deployment.Spec.Template.Spec.Containers[i].Resources = podContainer.Requests
							}
						}
					}
					deployment.Annotations["auto.request.operator/changed"] = "true"
					//deployment.Spec.Template.Annotations["auto.request.operator/changed"] = "true"
					r.UpdatePod(deployment, ctx)
				default:
					fmt.Printf("Could not find resource manager for type %s\n", pod.OwnerReferences[0].Kind)
				}

				// fmt.Printf("POD %s is managed by %s %s\n", pod.Name, ownerName, ownerKind)

			}
			err := deleteFromCache(cacheStore, SumPodRequest)
			if err != nil {
				log.Error(err, err.Error())
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *PodReconciler) UpdatePod(pod client.Object, ctx context.Context) (ctrl.Result, error) {
	if err := r.Update(ctx, pod); err != nil {
		if apierrors.IsConflict(err) {
			// The Pod has been updated since we read it.
			// Requeue the Pod to try to reconciliate again.
			return ctrl.Result{Requeue: true}, nil
		}
		if apierrors.IsNotFound(err) {
			// The Pod has been deleted since we read it.
			// Requeue the Pod to try to reconciliate again.
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "unable to update pod")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(r)
}
func RemoveLastChar(str string) string {
	for len(str) > 0 {
		_, size := utf8.DecodeLastRuneInString(str)
		return str[:len(str)-size]
	}
	return str
}
func GetPodRequests(pod corev1.Pod) PodRequests {
	containerData := []ContainerRequests{}
	for _, c := range pod.Spec.Containers {
		nanoCores, _ := strconv.Atoi(RemoveLastChar(c.Resources.Requests.Cpu().String()))
		memString := c.Resources.Requests.Memory().String()
		miMemory := 0
		if strings.Contains(memString, "Ki") {
			kkiMemory, _ := strconv.Atoi(strings.ReplaceAll(memString, "Ki", ""))
			miMemory = kkiMemory / 1000
		}
		if strings.Contains(memString, "Mi") {
			miMemory, _ = strconv.Atoi(strings.ReplaceAll(memString, "Mi", ""))
		}
		if strings.Contains(memString, "Gi") {
			giMemory, _ := strconv.Atoi(strings.ReplaceAll(memString, "Gi", ""))
			miMemory = giMemory / 1000
		}
		containerData = append(containerData, ContainerRequests{Name: c.Name, CPU: int64(nanoCores), Memory: int64(miMemory)})
	}
	return PodRequests{pod.Name, containerData, 0}
}

func addToCache(cacheStore cache.Store, object PodRequests) error {
	err := cacheStore.Add(object)
	if err != nil {
		klog.Errorf("failed to add key value to cache error", err)
		return err
	}
	return nil
}

func fetchFromCache(cacheStore cache.Store, key string) (PodRequests, error) {
	obj, exists, err := cacheStore.GetByKey(key)
	if err != nil {
		// klog.Errorf("failed to add key value to cache error", err)
		return PodRequests{}, err
	}
	if !exists {
		// klog.Errorf("object does not exist in the cache")
		err = errors.New("object does not exist in the cache")
		return PodRequests{}, err
	}
	return obj.(PodRequests), nil
}

func deleteFromCache(cacheStore cache.Store, object PodRequests) error {
	return cacheStore.Delete(object)
}

func GeneratePodRequestsObjectFromRestData(restData []byte) PodRequests {
	s := restData[:]
	data := PodMetricsRestData{}
	json.Unmarshal([]byte(s), &data)
	containerData := []ContainerRequests{}
	for _, c := range data.Containers {
		nanoCores, _ := strconv.Atoi(RemoveLastChar(c.Usage.CPU))
		kiMemory, _ := strconv.Atoi(strings.ReplaceAll(c.Usage.Memory, "Ki", ""))
		containerData = append(containerData, ContainerRequests{Name: c.Name, CPU: int64(nanoCores / 1000000), Memory: int64(kiMemory / 1000)})
	}
	return PodRequests{data.Metadata.Name, containerData, 0}
}
