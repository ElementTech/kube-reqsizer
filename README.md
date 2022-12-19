# kube-reqsizer
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/jatalocks/kube-reqsizer)](https://img.shields.io/github/v/release/jatalocks/kube-reqsizer)
<!-- [![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/kube-reqsizer)](https://artifacthub.io/packages/search?repo=kube-reqsizer) -->
![alt text](/assets/example.png)

**kube-reqsizer** is a kubernetes controller that will measure the usage of pods over time and optimize (reduce/increase) their requests based on the average usage.
## Deploy - Helm

```bash
helm repo add kube-reqsizer https://jatalocks.github.io/kube-reqsizer/
helm repo update
helm install kube-reqsizer/kube-reqsizer
```

**Core Values:**

```yaml
sampleSize: 1
minSeconds: 1
enableIncrease: true
enableReduce: true
maxMemory: 0
minMemory: 0
maxCPU: 0
minCPU: 0
enabledAnnotation: true
logLevel: info
```
## Prerequisites
- The metrics server must be deployed in your cluster. Read more about [Metrics Server](https://github.com/kubernetes-sigs/metrics-server). This controller uses the **metrics.k8s.io** extension API group (apis/metrics.k8s.io/v1beta1)

## Description

**kube-reqsizer** has primary custom flags:

```bash
--annotation-filter bool (default true)
    
    Enable a annotation filter for pod scraping. 
    Enabling this will ensure that the controller 
    only sets requests of controllers of which PODS or NAMESPACE 
    have the annotation set to "true".
    If "false", will ignore annotations and work on all pods in the cluster.

    # auto.request.operator/optimize=true
    # auto.request.operator/optimize=false

--sample-size int (default 1)

    The sample size to create an average from when reconciling.

--min-seconds int (default 1)

    Minimum seconds between pod restart.
    This ensures the controller will not restart a pod if the minimum time
    has not passed since it has started sampling it.

--enable-increase (default true)
    Enables the controller to increase pod requests

--enable-reduce (default true)
    Enables the controller to reduce pod requests

--max-cpu int (default 0)
    Maximum CPU in (m) that the controller can set a pod request to. 0 is infinite 

--max-memory int (default 0)
    Maximum memory in (Mi) that the controller can set a pod request to. 0 is infinite

--min-cpu int (default 0)
    Minimum CPU in (m) that the controller can set a pod request to. 0 is infinite

--min-memory int (default 0)
    Minimum memory in (Mi) that the controller can set a pod request to. 0 is infinite
```

### Annotations 
*If **annotation-filter** is **true**:*
```
auto.request.operator/optimize=true  # Optimize Pod/Namespace
auto.request.operator/optimize=false # Ignore Pod/Namespace
```
These are available POD/Namespace annotations *Regardless of **annotation-filter**:*
```
auto.request.operator/optimize=false # Ignore Pod/Namespace when optimizing entire cluster
auto.request.operator/mode=average   # Default Mode. Optimizes based on average. If ommited, mode is average
auto.request.operator/mode=max       # Sets the request to the MAXIMUM of all sample points
auto.request.operator/mode=min       # Sets the request to the MINIMUM of all sample points
```
### Disclaimer

`sample-size` is the amount of data-points the controller will store in cache before constructing an average for the pod. After a requests resizing, the cache will clean itself and a new average will be calculated based on the sample size. If `min-seconds` has not yet passed since the pod has last been supposed to be sample-reconciled, the controller will keep sampling the pod until `min-seconds` have been reached and only then zero the sample and restart from cache.

### Edge Cases

1. All samples in a certain cycle report `0` (less than 1):
   1. **mode=average**: The controller will ignore the pod and not reconcile.
   2. **mode=min**: The controller will put `1m` or `1Mi` as a value.
   3. **mode=max**: The controller will ignore the pod and not reconcile.
1. One or more of the samples in a certain cycle reports `0` (less than 1):
   1. **mode=average**: Will take the `0` into consideration.
   2. **mode=min**: Will consider the `0` as `1`.
   3. **mode=max**: Will take the `0` into consideration, but will ignore if *max=0*.
2. Annotation is `true` (`optimize=false` is as strong as **deny**):
   1. A namespace has `optimize=false` but a pod has `optimize=true`:
      1. The controller will ignore the pod and not reconcile.
   2. A namespace has `optimize=true` but a pod has `optimize=false`:
      1. The controller will ignore the pod and not reconcile.

## Limitations

- Does not work with CRD controllers (such as Argo Rollouts)
- Does not have persistent cache. Will reset cache on controller restart

# Development
## Getting Started
You’ll need a Kubernetes cluster to run against. You can use [KIND](https://sigs.k8s.io/kind) to get a local cluster for testing, or run against a remote cluster.
**Note:** Your controller will automatically use the current context in your kubeconfig file (i.e. whatever cluster `kubectl cluster-info` shows).

### Running on the cluster

1. Run the controller:
```sh
go run main.go
```

**OR**

2. Build and push your image to the location specified by `IMG`:
	
```sh
make docker-build docker-push IMG=<some-registry>/kube-reqsizer:tag
```
	
2. Deploy the controller to the cluster with the image specified by `IMG`:

```sh
make deploy IMG=<some-registry>/kube-reqsizer:tag
```
### Undeploy controller
UnDeploy the controller to the cluster:

```sh
make undeploy
```