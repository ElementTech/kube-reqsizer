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
	"fmt"
	"math"
	"time"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	ctrl "sigs.k8s.io/controller-runtime"
)

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch

const (
	operatorAnnotation     = "reqsizer.jatalocks.github.io/optimize"
	operatorModeAnnotation = "reqsizer.jatalocks.github.io/mode"
)

func cacheKeyFunc(obj interface{}) (string, error) {
	return obj.(PodRequests).Name, nil
}

var cacheStore = cache.NewTTLStore(cacheKeyFunc, 48*time.Hour)

// Reconcile handles a reconciliation request for a Pod.
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
			return ctrl.Result{}, err
		}
		log.Error(err, "unable to fetch Pod")
		return ctrl.Result{}, err
	}

	annotation, err := r.NamespaceOrPodHaveAnnotation(pod, ctx)
	if err != nil {
		log.Error(err, "failed to get annotations")
		return ctrl.Result{}, err
	}
	ignoreAnnotation, err := r.NamespaceOrPodHaveIgnoreAnnotation(pod, ctx)
	if err != nil {
		log.Error(err, "failed to get annotations")
		return ctrl.Result{}, err
	}

	if ((!r.EnableAnnotation) || (r.EnableAnnotation && annotation)) && !ignoreAnnotation {

		data, err := r.ClientSet.RESTClient().Get().AbsPath(fmt.Sprintf("apis/metrics.k8s.io/v1beta1/namespaces/%v/pods/%v", pod.Namespace, pod.Name)).DoRaw(ctx)

		if err != nil {
			log.Error(err, "failed to get stats from pod")
			return ctrl.Result{}, err
		}
		PodUsageData := GeneratePodRequestsObjectFromRestData(data)

		SumPodRequest := PodRequests{Name: pod.Name, ContainerRequests: []ContainerRequests{}}

		SumPodRequest.ContainerRequests = PodUsageData.ContainerRequests

		LatestPodRequest, err := fetchFromCache(cacheStore, pod.Name)
		if err != nil {
			SumPodRequest.Sample = 0
			SumPodRequest.TimeSinceFirstSample = 0
			SumPodRequest.Timestamp = time.Now()
			log.Info(fmt.Sprint("Adding cache sample ", SumPodRequest.Sample))
			addToCache(cacheStore, SumPodRequest)
			log.Info(fmt.Sprint("Items in Cache: ", len(cacheStore.List())))
		} else {
			if err != nil {
				log.Error(err, err.Error())
			} else {
				SumPodRequest.Sample = LatestPodRequest.Sample + 1
				log.Info(fmt.Sprint(time.Now(), LatestPodRequest.Timestamp))
				SumPodRequest.TimeSinceFirstSample = time.Since(LatestPodRequest.Timestamp).Seconds()
				// SumPodRequest.Timestamp = time.Now()
				log.Info(fmt.Sprint("Updating cache sample ", SumPodRequest.Sample))

				for _, sumC := range SumPodRequest.ContainerRequests {
					for _, latestC := range LatestPodRequest.ContainerRequests {
						if latestC.Name == sumC.Name {
							sumCAddr := &sumC
							if latestC.CPU > 0 {
								sumCAddr.MaxCPU = int64(math.Max(float64(sumCAddr.MaxCPU), float64(latestC.CPU)))
								sumCAddr.MinCPU = int64(math.Min(float64(sumCAddr.MinCPU), float64(latestC.CPU)))
							} else {
								// sumCAddr.MaxCPU = latestC.CPU
								sumCAddr.MinCPU = 1
							}
							if latestC.Memory > 0 {
								sumCAddr.MaxMemory = int64(math.Max(float64(sumCAddr.MaxMemory), float64(latestC.Memory)))
								sumCAddr.MinMemory = int64(math.Min(float64(sumCAddr.MinMemory), float64(latestC.Memory)))
							} else {
								// sumCAddr.MaxMemory = latestC.Memory
								sumCAddr.MinMemory = 1
							}
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
		log.Info(fmt.Sprint(SumPodRequest))
		if (SumPodRequest.Sample >= r.SampleSize) && (SumPodRequest.TimeSinceFirstSample >= r.MinSecondsBetweenPodRestart) {
			log.Info("Sample Size and Minimum Time have been reached")
			PodChange := false
			Requests := []NewContainerRequests{}
			for _, c := range SumPodRequest.ContainerRequests {
				AverageUsageCPU := c.CPU / int64(SumPodRequest.Sample)
				AverageUsageMemory := c.Memory / int64(SumPodRequest.Sample)
				PodRequestsData := GetPodRequests(pod)
				for _, currentC := range PodRequestsData.ContainerRequests {
					if currentC.Name == c.Name {
						for i, v := range pod.Spec.Containers {
							if v.Name == c.Name {
								log.Info(c.Name)
								log.Info(fmt.Sprint("Comparing CPU: ", fmt.Sprintf("%dm", AverageUsageCPU), " <> ", fmt.Sprintf("%dm", currentC.CPU)))
								log.Info(fmt.Sprint("Comparing Memory: ", fmt.Sprintf("%dMi", AverageUsageMemory), " <> ", fmt.Sprintf("%dMi", currentC.Memory)))
								if pod.Spec.Containers[i].Resources.Requests != nil {
									switch r.GetPodMode(pod, ctx) {
									case "average":
										if r.ValidateCPU(currentC.CPU, AverageUsageCPU) {
											pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", int(float64(AverageUsageCPU)*r.CPUFactor)))
											PodChange = true
										}
									case "min":
										if r.ValidateCPU(currentC.CPU, c.MinCPU) {
											pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", int(float64(c.MinCPU)*r.CPUFactor)))
											PodChange = true
										}
									case "max":
										if r.ValidateCPU(currentC.CPU, c.MaxCPU) {
											pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", int(float64(c.MaxCPU)*r.CPUFactor)))
											PodChange = true
										}
									}
								}
								if AverageUsageMemory > 0 {
									if pod.Spec.Containers[i].Resources.Requests != nil {
										switch r.GetPodMode(pod, ctx) {
										case "average":
											if r.ValidateMemory(currentC.Memory, AverageUsageMemory) {
												pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", int(float64(AverageUsageMemory)*r.MemoryFactor)))
												PodChange = true
											}
										case "min":
											if r.ValidateMemory(currentC.Memory, c.MinMemory) {
												pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", int(float64(c.MinMemory)*r.MemoryFactor)))
												PodChange = true
											}
										case "max":
											if r.ValidateMemory(currentC.Memory, c.MaxMemory) {
												pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", int(float64(c.MaxMemory)*r.MemoryFactor)))
												PodChange = true
											}
										}
									}
								}
								Requests = append(Requests, NewContainerRequests{Name: c.Name, Requests: pod.Spec.Containers[i].Resources})
							}
						}
					}
				}
			}
			if PodChange {
				pod.Annotations["reqsizer.jatalocks.github.io/changed"] = "true"
				log.Info("Pod Requests Will Change")

				if len(pod.OwnerReferences) == 0 {
					log.Info("Pod has no owner")
					return r.UpdateKubeObject(&pod, ctx)
				}

				var ownerName string
				switch pod.OwnerReferences[0].Kind {
				case "ReplicaSet":
					replica, err := r.ClientSet.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, pod.OwnerReferences[0].Name, metav1.GetOptions{})
					if err != nil {
						log.Error(err, err.Error())
						return ctrl.Result{}, err
					}

					ownerName = replica.OwnerReferences[0].Name
					if replica.OwnerReferences[0].Kind == "Deployment" {
						log.Info("Is Owned by Deployment")
						deployment, err := r.ClientSet.AppsV1().Deployments(pod.Namespace).Get(ctx, ownerName, metav1.GetOptions{})
						if err != nil {
							log.Error(err, err.Error())
							return ctrl.Result{}, err
						}
						UpdatePodController(&deployment.Spec.Template.Spec, Requests, ctx)
						deployment.Annotations["reqsizer.jatalocks.github.io/changed"] = "true"

						return r.UpdateKubeObject(deployment, ctx)
					} else {
						log.Info("Is Owned by Unknown CRD")
						return ctrl.Result{}, nil
					}
				case "DaemonSet":
					log.Info("Is Owned by DaemonSet")
					ownerName = pod.OwnerReferences[0].Name

					deployment, err := r.ClientSet.AppsV1().DaemonSets(pod.Namespace).Get(ctx, ownerName, metav1.GetOptions{})
					if err != nil {
						log.Error(err, err.Error())
						return ctrl.Result{}, err
					}
					UpdatePodController(&deployment.Spec.Template.Spec, Requests, ctx)
					deployment.Annotations["reqsizer.jatalocks.github.io/changed"] = "true"
					return r.UpdateKubeObject(deployment, ctx)
				case "StatefulSet":
					log.Info("Is Owned by StatefulSet")
					ownerName = pod.OwnerReferences[0].Name

					deployment, err := r.ClientSet.AppsV1().StatefulSets(pod.Namespace).Get(ctx, ownerName, metav1.GetOptions{})
					if err != nil {
						log.Error(err, err.Error())
						return ctrl.Result{}, err
					}

					UpdatePodController(&deployment.Spec.Template.Spec, Requests, ctx)
					deployment.Annotations["reqsizer.jatalocks.github.io/changed"] = "true"
					return r.UpdateKubeObject(deployment, ctx)
				default:
					fmt.Printf("Could not find resource manager for type %s\n", pod.OwnerReferences[0].Kind)
				}

			}
			err := deleteFromCache(cacheStore, SumPodRequest)
			if err != nil {
				log.Error(err, err.Error())
			}
		}
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}
