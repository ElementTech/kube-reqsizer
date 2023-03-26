package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jatalocks/kube-reqsizer/types"
	"github.com/labstack/gommon/log"

	corev1 "k8s.io/api/core/v1" // nolint:all
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

func (r *PodReconciler) NamespaceOrPodHaveAnnotation(pod *corev1.Pod, ctx context.Context) (bool, error) {
	podHasAnnotation := pod.Annotations[operatorAnnotation] == "true"
	namespace, err := r.ClientSet.CoreV1().Namespaces().Get(ctx, pod.Namespace, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	namespaceHasAnnotation := namespace.Annotations[operatorAnnotation] == "true"
	return (podHasAnnotation || namespaceHasAnnotation), nil
}

func (r *PodReconciler) NamespaceOrPodHaveIgnoreAnnotation(pod *corev1.Pod, ctx context.Context) (bool, error) {
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
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		if apierrors.IsNotFound(err) {
			// The Pod has been deleted since we read it.
			// Requeue the Pod to try to reconciliate again.
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to update pod")
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func UpdatePodController(podspec *corev1.PodSpec, Requests []types.NewContainerRequests, ctx context.Context) {
	for _, podContainer := range Requests {
		for i, depContainer := range podspec.Containers {
			if depContainer.Name == podContainer.Name {
				// log.Info(fmt.Sprint("Setting", podContainer.Requests))
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
					// From 5 to 10
					// ((10-5)/10 * 100) >= 50%
					return ((float64(AverageUsageCPU-currentCPU) / float64(currentCPU)) * 100) >= float64(r.MinCPUIncreasePercentage)
				}
			}
		} else {
			if r.EnableReduce {
				if (AverageUsageCPU >= r.MinCPU) || (r.MinCPU == 0) {
					// From 10 to 5
					// ((10-5)/10 * 100) >= 50%
					return ((float64(currentCPU-AverageUsageCPU) / float64(currentCPU)) * 100) >= float64(r.MinCPUDecreasePercentage)
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
					return ((float64(AverageUsageMemory-currentMemory) / float64(currentMemory)) * 100) >= float64(r.MinMemoryIncreasePercentage)
				}
			}
		} else {
			if r.EnableReduce {
				if (AverageUsageMemory >= r.MinMemory) || (r.MinMemory == 0) {
					return ((float64(currentMemory-AverageUsageMemory) / float64(currentMemory)) * 100) >= float64(r.MinMemoryDecreasePercentage)
				}
			}
		}
	}
	return false
}

func GetPodRequests(pod corev1.Pod) types.PodRequests {
	containerData := []types.ContainerRequests{}
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
		containerData = append(containerData, types.ContainerRequests{Name: c.Name, CPU: int64(nanoCores), Memory: int64(miMemory)})
	}
	return types.PodRequests{Name: pod.Name, Namespace: pod.Namespace, ContainerRequests: containerData, Sample: 0}
}

func GeneratePodRequestsObjectFromRestData(restData []byte) types.PodRequests {
	s := restData[:]
	data := types.PodMetricsRestData{}
	json.Unmarshal([]byte(s), &data)
	containerData := []types.ContainerRequests{}
	for _, c := range data.Containers {
		nanoCores, _ := strconv.Atoi(RemoveLastChar(c.Usage.CPU))
		kiMemory, _ := strconv.Atoi(strings.ReplaceAll(c.Usage.Memory, "Ki", ""))
		containerData = append(containerData, types.ContainerRequests{Name: c.Name, CPU: int64(nanoCores / 1000000), Memory: int64(kiMemory / 1000)})
	}
	return types.PodRequests{Name: data.Metadata.Name, Namespace: data.Metadata.Namespace, ContainerRequests: containerData, Sample: 0}
}

func (r *PodReconciler) MinimumUptimeOfPodInParent(pod corev1.Pod, ctx context.Context) bool {

	if len(pod.OwnerReferences) == 0 {
		return time.Since(pod.CreationTimestamp.Time).Seconds() >= r.MinSecondsBetweenPodRestart
	}
	_, _, deploymentName, err := r.GetPodParentKind(pod, ctx)
	if err != nil {
		log.Error(err)
		return false
	}

	labelArray := []string{"app", "app.kubernetes.io/name", "app.kubernetes.io/instance", "app.kubernetes.io/component"}
	overAllLength := 0
	for _, l := range labelArray {
		options := metav1.ListOptions{
			LabelSelector: l + " in (" + deploymentName + ")",
		}
		podList, _ := r.ClientSet.CoreV1().Pods(pod.Namespace).List(ctx, options)

		overAllLength += len((*podList).Items)
		for _, podInfo := range (*podList).Items {
			if time.Since(podInfo.CreationTimestamp.Time).Seconds() < r.MinSecondsBetweenPodRestart {
				return false
			}
		}
	}
	return overAllLength != 0
}

func (r *PodReconciler) GetPodParentKind(pod corev1.Pod, ctx context.Context) (*v1.PodSpec, interface{}, string, error) {
	if len(pod.OwnerReferences) > 0 {
		switch pod.OwnerReferences[0].Kind {
		case "ReplicaSet":
			replica, err := r.ClientSet.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, pod.OwnerReferences[0].Name, metav1.GetOptions{})
			if err != nil {
				log.Error(err, err.Error())
				return nil, nil, "", err
			}
			deployment, err := r.ClientSet.AppsV1().Deployments(pod.Namespace).Get(ctx, replica.OwnerReferences[0].Name, metav1.GetOptions{})
			if replica.OwnerReferences[0].Kind == "Deployment" {
				return &deployment.Spec.Template.Spec, deployment, deployment.Name, err
			} else {
				return nil, nil, "", errors.New("is Owned by Unknown CRD")
			}
		case "DaemonSet":
			deployment, err := r.ClientSet.AppsV1().DaemonSets(pod.Namespace).Get(ctx, pod.OwnerReferences[0].Name, metav1.GetOptions{})
			return &deployment.Spec.Template.Spec, deployment, deployment.Name, err
		case "StatefulSet":
			deployment, err := r.ClientSet.AppsV1().StatefulSets(pod.Namespace).Get(ctx, pod.OwnerReferences[0].Name, metav1.GetOptions{})
			return &deployment.Spec.Template.Spec, deployment, deployment.Name, err
		default:
			return nil, nil, "", errors.New("is Owned by Unknown CRD")
		}
	} else {
		return nil, nil, "", errors.New("pod Has No Owner")
	}
}

func (r *PodReconciler) GetPodCacheName(pod *corev1.Pod) string {
	val, ok := pod.Labels["app"]
	if !ok {
		val, ok = pod.Labels["app.kubernetes.io/name"]
		if !ok {
			val, ok = pod.Labels["app.kubernetes.io/instance"]
			if !ok {
				val, ok = pod.Labels["app.kubernetes.io/component"]
				if !ok {
					val = strings.Split(pod.Name, "-")[0]
				}
			}
		}
	}
	return val
}
