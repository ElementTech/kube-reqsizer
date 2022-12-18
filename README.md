# kube-reqsizer
![alt text](/assets/example.png)

**kube-reqsizer** is a kubernetes controller that will measure the usage of pods over time and optimize (reduce/improve) their requests based on the average usage.
## Deploy - Helm

```bash
helm install \
    https://github.com/jatalocks/kube-reqsizer/releases/download/kube-reqsizer-0.4.0/kube-reqsizer-0.4.0.tgz
```

**Core Values:**

```yaml
sampleSize: 1
minSeconds: 1
enabledAnnotation: true
logLevel: info
```
## Description

**kube-reqsizer** has three primary custom flags:

```
--annotation-filter bool (default true)
    
    Enable a annotation filter for pod scraping. 
    Enabling this will ensure that the controller 
    only sets requests of controllers of which PODS or NAMESPACE have the annotation. 
    If false, will work on all pods in the cluster.

    auto.request.operator/optimize=true

--sample-size int (default 1)

    The sample size to create an average from when reconciling.

--min-seconds int (default 1)

    Minimum seconds between pod restart.
    This ensures the controller will not restart a pod if the minimum time
    has not passed since it has started sampling it.
```

Disclaimer: 

`sample-size` is the amount of data-points the controller will store in cache before constructing an average for the pod. After a requests resizing, the cache will clean itself and a new average will be calculated based on the sample size. If `min-seconds` has not yet passed since the pod has last been supposed to be sample-reconciled, the controller will keep sampling the pod until `min-seconds` have been reached and only then zero the sample and restart from cache.

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