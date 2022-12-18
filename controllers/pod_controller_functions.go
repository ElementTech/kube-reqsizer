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
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (r *PodReconciler) UpdateKubeObject(pod client.Object, ctx context.Context) (ctrl.Result, error) {
	if err := r.Update(ctx, pod); err != nil {
		if apierrors.IsConflict(err) {
			// The Pod has been updated since we read it.
			// Requeue the Pod to try to reconciliate again.
			return ctrl.Result{Requeue: true}, err
		}
		if apierrors.IsNotFound(err) {
			// The Pod has been deleted since we read it.
			// Requeue the Pod to try to reconciliate again.
			return ctrl.Result{}, err
		}
		log.Error(err, "unable to update pod")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func UpdatePodController(deployment interface{}, Requests []NewContainerRequests, ctx context.Context) {

	switch v := deployment.(type) {
	case *v1.Deployment:
		for _, podContainer := range Requests {
			for i, depContainer := range v.Spec.Template.Spec.Containers {
				if depContainer.Name == podContainer.Name {
					log.Info(fmt.Sprint("Setting", podContainer.Requests))
					v.Spec.Template.Spec.Containers[i].Resources = podContainer.Requests
				}
			}
		}
		v.Annotations["auto.request.operator/changed"] = "true"
	case *v1.DaemonSet:
		for _, podContainer := range Requests {
			for i, depContainer := range v.Spec.Template.Spec.Containers {
				if depContainer.Name == podContainer.Name {
					log.Info(fmt.Sprint("Setting", podContainer.Requests))
					v.Spec.Template.Spec.Containers[i].Resources = podContainer.Requests
				}
			}
		}
		v.Annotations["auto.request.operator/changed"] = "true"
	case *v1.StatefulSet:
		for _, podContainer := range Requests {
			for i, depContainer := range v.Spec.Template.Spec.Containers {
				if depContainer.Name == podContainer.Name {
					log.Info(fmt.Sprint("Setting", podContainer.Requests))
					v.Spec.Template.Spec.Containers[i].Resources = podContainer.Requests
				}
			}
		}
		v.Annotations["auto.request.operator/changed"] = "true"
	default:
		log.Debug("Unsupported type")
	}

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
