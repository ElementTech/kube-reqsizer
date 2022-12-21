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

	"github.com/labstack/gommon/log"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

func (r *PodReconciler) NamespaceOrPodHaveAnnotation(pod corev1.Pod, ctx context.Context) (bool, error) {
	podHasAnnotation := pod.Annotations[operatorAnnotation] == "true"
	namespace, err := r.ClientSet.CoreV1().Namespaces().Get(ctx, pod.Namespace, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	namespaceHasAnnotation := namespace.Annotations[operatorAnnotation] == "true"
	return (podHasAnnotation || namespaceHasAnnotation), nil
}

func (r *PodReconciler) NamespaceOrPodHaveIgnoreAnnotation(pod corev1.Pod, ctx context.Context) (bool, error) {
	podHasIgnoreAnnotation := pod.Annotations[operatorAnnotation] == "false"
	namespace, err := r.ClientSet.CoreV1().Namespaces().Get(ctx, pod.Namespace, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	namespaceHasIgnoreAnnotation := namespace.Annotations[operatorAnnotation] == "false"
	return (namespaceHasIgnoreAnnotation) || (podHasIgnoreAnnotation), nil
}

func (r *PodReconciler) GetPodMode(pod corev1.Pod, ctx context.Context) string {
	podHasAverage := pod.Annotations[operatorModeAnnotation] == "average"
	podHasMin := pod.Annotations[operatorModeAnnotation] == "min"
	podHasMax := pod.Annotations[operatorModeAnnotation] == "max"
	namespace, err := r.ClientSet.CoreV1().Namespaces().Get(ctx, pod.Namespace, metav1.GetOptions{})
	if err != nil {
		return "average"
	}
	namespaceHasAverage := namespace.Annotations[operatorModeAnnotation] == "average"
	namespaceHasMin := namespace.Annotations[operatorModeAnnotation] == "min"
	namespaceHasMax := namespace.Annotations[operatorModeAnnotation] == "max"
	if podHasAverage || namespaceHasAverage {
		return "average"
	}
	if (podHasMax || namespaceHasMax) && !(podHasMin || namespaceHasMin) {
		return "max"
	}
	if (podHasMin || namespaceHasMin) && !(podHasMax || namespaceHasMax) {
		return "max"
	}
	return "average"
}

func (r *PodReconciler) UpdateKubeObject(pod client.Object, ctx context.Context) (ctrl.Result, error) {
	if err := r.Update(ctx, pod); err != nil {
		if apierrors.IsConflict(err) {
			// The Pod has been updated since we read it.
			// Requeue the Pod to try to reconciliate again.
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		if apierrors.IsNotFound(err) {
			// The Pod has been deleted since we read it.
			// Requeue the Pod to try to reconciliate again.
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to update pod")
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func UpdatePodController(podspec *corev1.PodSpec, Requests []NewContainerRequests, ctx context.Context) {
	for _, podContainer := range Requests {
		for i, depContainer := range podspec.Containers {
			if depContainer.Name == podContainer.Name {
				log.Info(fmt.Sprint("Setting", podContainer.Requests))
				podspec.Containers[i].Resources = podContainer.Requests
			}
		}
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager, concurrentWorkers uint) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: int(concurrentWorkers),
			RecoverPanic:            true,
		}).
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

func (r *PodReconciler) ValidateCPU(currentCPU, AverageUsageCPU int64) bool {
	if AverageUsageCPU > 0 {
		if AverageUsageCPU > currentCPU {
			if r.EnableIncrease {
				if (AverageUsageCPU <= r.MaxCPU) || (r.MaxCPU == 0) {
					return true
				}
			}
		} else {
			if r.EnableReduce {
				if (AverageUsageCPU >= r.MinCPU) || (r.MinCPU == 0) {
					return true
				}
			}
		}
	}
	return false
}

func (r *PodReconciler) ValidateMemory(currentMemory, AverageUsageMemory int64) bool {
	if AverageUsageMemory > 0 {
		if AverageUsageMemory > currentMemory {
			if r.EnableIncrease {
				if (AverageUsageMemory <= r.MaxMemory) || (r.MaxMemory == 0) {
					return true
				}
			}
		} else {
			if r.EnableReduce {
				if (AverageUsageMemory >= r.MinMemory) || (r.MinMemory == 0) {
					return true
				}
			}
		}
	}
	return false
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
	return PodRequests{pod.Name, containerData, 0, 0, time.Now()}
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
	return PodRequests{data.Metadata.Name, containerData, 0, 0, time.Now()}
}
