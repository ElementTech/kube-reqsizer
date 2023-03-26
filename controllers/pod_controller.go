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

	"github.com/go-logr/logr"
	"github.com/jatalocks/kube-reqsizer/pkg/cache/localcache"
	"github.com/jatalocks/kube-reqsizer/pkg/cache/rediscache"

	"github.com/jatalocks/kube-reqsizer/pkg/git"
	"github.com/jatalocks/kube-reqsizer/types"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1" // nolint:all
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;update;patch

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Log                         logr.Logger
	Scheme                      *runtime.Scheme
	ClientSet                   *kubernetes.Clientset
	SampleSize                  int
	EnableAnnotation            bool
	MinSecondsBetweenPodRestart float64
	EnableIncrease              bool
	EnableReduce                bool
	MaxMemory                   int64
	MaxCPU                      int64
	MinMemory                   int64
	MinCPU                      int64
	MinCPUIncreasePercentage    int64
	MinMemoryIncreasePercentage int64
	MinCPUDecreasePercentage    int64
	MinMemoryDecreasePercentage int64
	CPUFactor                   float64
	MemoryFactor                float64
	RedisClient                 rediscache.RedisClient
	EnablePersistence           bool
	GithubMode                  bool
	VerboseMode                 bool
}

const (
	operatorAnnotation     = "reqsizer.jatalocks.github.io/optimize"
	operatorModeAnnotation = "reqsizer.jatalocks.github.io/mode"
)

var (
	cpuOffset = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace:   "kube_reqsizer",
			Name:        "cpu_offset",
			Help:        "Number of milli-cores that have been increased/removed since startup",
			ConstLabels: map[string]string{},
		},
	)
	memoryOffset = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "kube_reqsizer",
			Name:      "memory_offset",
			Help:      "Number of megabits that have been increased/removed since startup",
		},
	)
	cacheSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "kube_reqsizer",
			Name:      "cache_size",
			Help:      "Number of pod controllers currently in cache",
		},
	)
)

func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(cpuOffset, memoryOffset, cacheSize)
}

func cacheKeyFunc(obj interface{}) (string, error) {
	return obj.(types.PodRequests).Name, nil
}

var cacheStore = cache.NewStore(cacheKeyFunc)

// Reconcile handles a reconciliation request for a Pod.
func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("pod", req.NamespacedName)
	if r.EnablePersistence {
		cacheSize.Set(float64(r.RedisClient.CacheSize()))
	} else {
		cacheSize.Set(float64(len(cacheStore.List())))
	}
	/*
		Step 0: Fetch the Pod from the Kubernetes API.
	*/
	var pod corev1.Pod

	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(nil, "unable to fetch Pod")
		return ctrl.Result{}, err
	}
	podReferenceName := r.GetPodCacheName(&pod) + "-" + pod.Namespace
	annotation, err := r.NamespaceOrPodHaveAnnotation(&pod, ctx)
	if err != nil {
		log.Error(nil, "failed to get annotations")
		return ctrl.Result{}, err
	}
	ignoreAnnotation, err := r.NamespaceOrPodHaveIgnoreAnnotation(&pod, ctx)
	if err != nil {
		log.Error(nil, "failed to get annotations")
		return ctrl.Result{}, err
	}

	if ((!r.EnableAnnotation) || (r.EnableAnnotation && annotation)) && !ignoreAnnotation {
		log.Info("Cache Reference Name: " + podReferenceName)

		data, err := r.ClientSet.RESTClient().Get().AbsPath(fmt.Sprintf("apis/metrics.k8s.io/v1beta1/namespaces/%v/pods/%v", pod.Namespace, pod.Name)).DoRaw(ctx)

		if err != nil {
			log.Error(nil, "failed to get stats from pod")
			return ctrl.Result{}, err
		}
		PodUsageData := GeneratePodRequestsObjectFromRestData(data)
		SumPodRequest := types.PodRequests{Name: podReferenceName, Namespace: pod.Namespace, ContainerRequests: []types.ContainerRequests{}}

		SumPodRequest.ContainerRequests = PodUsageData.ContainerRequests
		var LatestPodRequest types.PodRequests
		if r.EnablePersistence {
			LatestPodRequest, err = r.RedisClient.FetchFromCache(podReferenceName)
		} else {
			LatestPodRequest, err = localcache.FetchFromCache(cacheStore, podReferenceName)
		}

		if err != nil {
			SumPodRequest.Sample = 0
			log.Info(fmt.Sprint("Adding cache sample ", SumPodRequest.Sample))
			if r.EnablePersistence {
				r.RedisClient.AddToCache(SumPodRequest)
				log.Info(fmt.Sprint("Items in Cache: ", r.RedisClient.CacheSize()))
			} else {
				localcache.AddToCache(cacheStore, SumPodRequest)
				log.Info(fmt.Sprint("Items in Cache: ", len(cacheStore.List())))
			}
		} else {
			if r.EnablePersistence {
				log.Info(fmt.Sprint("Items in Cache: ", r.RedisClient.CacheSize()))
			} else {
				log.Info(fmt.Sprint("Items in Cache: ", len(cacheStore.List())))
			}
			SumPodRequest.Sample = LatestPodRequest.Sample + 1

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

			if r.EnablePersistence {
				if err := r.RedisClient.DeleteFromCache(LatestPodRequest); err != nil {
					log.Error(err, err.Error())
				}
				if err = r.RedisClient.AddToCache(SumPodRequest); err != nil {
					log.Error(err, err.Error())
				}
			} else {
				if err := localcache.DeleteFromCache(cacheStore, LatestPodRequest); err != nil {
					log.Error(err, err.Error())
				}
				if err = localcache.AddToCache(cacheStore, SumPodRequest); err != nil {
					log.Error(err, err.Error())
				}
			}
		}
		if (SumPodRequest.Sample >= r.SampleSize) && r.MinimumUptimeOfPodInParent(pod, ctx) {
			log.Info("Sample Size and Minimum Time have been reached")
			PodChange := false
			Requests := []types.NewContainerRequests{}
			for _, c := range SumPodRequest.ContainerRequests {
				AverageUsageCPU := c.CPU / int64(SumPodRequest.Sample)
				AverageUsageMemory := c.Memory / int64(SumPodRequest.Sample)
				PodRequestsData := GetPodRequests(pod)
				for _, currentC := range PodRequestsData.ContainerRequests {
					if currentC.Name == c.Name {
						for i, v := range pod.Spec.Containers {
							if v.Name == c.Name {
								if AverageUsageCPU < r.MinCPU && (r.MinCPU > 0) {
									AverageUsageCPU = r.MinCPU
								}
								if AverageUsageCPU > r.MaxCPU && (r.MaxCPU > 0) {
									AverageUsageCPU = r.MaxCPU
								}
								if AverageUsageMemory < r.MinMemory && (r.MinMemory > 0) {
									AverageUsageMemory = r.MinMemory
								}
								if AverageUsageMemory > r.MaxMemory && (r.MaxMemory > 0) {
									AverageUsageMemory = r.MaxMemory
								}
								log.Info(fmt.Sprint(c.Name, " Comparing CPU: ", fmt.Sprintf("%dm", AverageUsageCPU), " <> ", fmt.Sprintf("%dm", currentC.CPU)))
								log.Info(fmt.Sprint(c.Name, " Comparing Memory: ", fmt.Sprintf("%dMi", AverageUsageMemory), " <> ", fmt.Sprintf("%dMi", currentC.Memory)))
								if pod.Spec.Containers[i].Resources.Requests != nil {
									switch r.GetPodMode(pod, ctx) {
									case "average":
										if r.ValidateCPU(currentC.CPU, AverageUsageCPU) {
											pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", int(float64(AverageUsageCPU)*r.CPUFactor)))
											cpuOffset.Add(float64(int(float64(AverageUsageCPU)*r.CPUFactor) - int(currentC.CPU)))
											PodChange = true
										}
										if r.ValidateMemory(currentC.Memory, AverageUsageMemory) {
											pod.Spec.Containers[i].Resources.Requests[v1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%dMi", int(float64(AverageUsageMemory)*r.MemoryFactor)))
											memoryOffset.Add(float64(int(float64(AverageUsageMemory)*r.MemoryFactor) - int(currentC.Memory)))
											PodChange = true
										}
									case "min":
										if r.ValidateCPU(currentC.CPU, c.MinCPU) {
											pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", int(float64(c.MinCPU)*r.CPUFactor)))
											cpuOffset.Add(float64(int(float64(c.MinCPU)*r.CPUFactor) - int(currentC.CPU)))
											PodChange = true
										}
										if r.ValidateMemory(currentC.Memory, c.MinMemory) {
											pod.Spec.Containers[i].Resources.Requests[v1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%dMi", int(float64(c.MinMemory)*r.MemoryFactor)))
											memoryOffset.Add(float64(int(float64(c.MinMemory)*r.MemoryFactor) - int(currentC.Memory)))
											PodChange = true
										}
									case "max":
										if r.ValidateCPU(currentC.CPU, c.MaxCPU) {
											pod.Spec.Containers[i].Resources.Requests[v1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", int(float64(c.MaxCPU)*r.CPUFactor)))
											cpuOffset.Add(float64(int(float64(c.MaxCPU)*r.CPUFactor) - int(currentC.CPU)))
											PodChange = true
										}
										if r.ValidateMemory(currentC.Memory, c.MaxMemory) {
											pod.Spec.Containers[i].Resources.Requests[v1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%dMi", int(float64(c.MaxMemory)*r.MemoryFactor)))
											memoryOffset.Add(float64(int(float64(c.MaxMemory)*r.MemoryFactor) - int(currentC.Memory)))
											PodChange = true
										}
									}
								}

								Requests = append(Requests, types.NewContainerRequests{Name: c.Name, Requests: pod.Spec.Containers[i].Resources})
							}
						}
					}
				}
			}
			if r.EnablePersistence {
				if err := r.RedisClient.DeleteFromCache(SumPodRequest); err != nil {
					log.Error(err, err.Error())
				}
			} else {
				if err := localcache.DeleteFromCache(cacheStore, LatestPodRequest); err != nil {
					log.Error(err, err.Error())
				}
			}
			if PodChange {
				pod.Annotations["reqsizer.jatalocks.github.io/changed"] = "true"

				log.Info("Pod Requests Will Change")

				if len(pod.OwnerReferences) == 0 {
					log.Info("Pod has no owner")
					return r.UpdateKubeObject(&pod, ctx)
				}

				podSpec, deployment, _, err := r.GetPodParentKind(pod, ctx)
				if err != nil {
					return ctrl.Result{}, err
				}
				if !r.VerboseMode {
					UpdatePodController(podSpec, Requests, ctx)
					return r.UpdateKubeObject(deployment.(client.Object), ctx)
				}
				if r.GithubMode {
					path, pathExists := pod.Annotations["reqsizer.jatalocks.github.io/github.path"]
					repo, repoExists := pod.Annotations["reqsizer.jatalocks.github.io/github.repo"]
					owner, ownerExists := pod.Annotations["reqsizer.jatalocks.github.io/github.owner"]
					if pathExists && repoExists && ownerExists {
						if err := git.UpdateContainerRequestsInFile(path, Requests, repo, owner); err != nil {
							log.Error(err, err.Error())
						}
					}
				} else {
					log.Info("Pod is missing github annotations")
				}
			}
		}
	}

	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}
