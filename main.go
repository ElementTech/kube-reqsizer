/*
Copyright 2022.

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

package main

import (
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	"github.com/go-redis/redis"
	"github.com/jatalocks/kube-reqsizer/controllers"
	"github.com/jatalocks/kube-reqsizer/pkg/cache/rediscache"
	"github.com/labstack/gommon/log"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var enableAnnotation bool
	var probeAddr string
	var sampleSize int
	var minSecondsBetweenPodRestart float64
	var enableIncrease bool
	var enableReduce bool
	var maxMemory int64
	var maxCPU int64
	var minMemory int64
	var minCPU int64
	var minCPUIncreasePercentage int64
	var minCPUDecreasePercentage int64
	var minMemoryIncreasePercentage int64
	var minMemoryDecreasePercentage int64
	var concurrentWorkers uint

	var cpuFactor float64
	var memoryFactor float64

	var enablePersistence bool
	var redisHost string
	var redisPassword string
	var redisPort string
	var redisDB uint

	var githubMode bool
	var verboseMode bool

	flag.BoolVar(&enablePersistence, "enable-persistence", false, "Uses Redis as a persistent cache")
	flag.BoolVar(&enablePersistence, "github-mode", false, "Creates pull requests for resource changes using annotations")
	flag.BoolVar(&enablePersistence, "verbose-mode", false, "Disable real-time resource applies. Logs only.")

	flag.StringVar(&redisHost, "redis-host", "localhost", "Redis host address")
	flag.StringVar(&redisPort, "redis-port", "6379", "Redis port")
	flag.StringVar(&redisPassword, "redis-password", "", "Redis password")
	flag.UintVar(&redisDB, "redis-db", 0, "Redis DB number")

	flag.Int64Var(&minCPUIncreasePercentage, "min-cpu-increase-percentage", 0, "Minimum difference in percentage that is required to increase CPU requests")
	flag.Int64Var(&minMemoryIncreasePercentage, "min-memory-increase-percentage", 0, "Minimum difference in percentage that is required to increase Memory requests")
	flag.Int64Var(&minCPUDecreasePercentage, "min-cpu-decrease-percentage", 0, "Minimum difference in percentage that is required to decrease CPU requests")
	flag.Int64Var(&minMemoryDecreasePercentage, "min-memory-decrease-percentage", 0, "Minimum difference in percentage that is required to decrease Memory requests")

	flag.BoolVar(&enableIncrease, "enable-increase", true, "Enables the controller to increase pod requests")
	flag.BoolVar(&enableReduce, "enable-reduce", true, "Enables the controller to reduce pod requests")
	flag.Int64Var(&maxMemory, "max-memory", 0, "Maximum memory in (Mi) that the controller can set a pod request to. 0 is infinite")
	flag.Int64Var(&maxCPU, "max-cpu", 0, "Maximum CPU in (m) that the controller can set a pod request to. 0 is infinite")
	flag.Float64Var(&cpuFactor, "cpu-factor", 1, "A factor to multiply CPU requests when reconciling. 1 By default.")
	flag.Float64Var(&memoryFactor, "memory-factor", 1, "A factor to multiply Memory requests when reconciling. 1 By default.")
	flag.UintVar(&concurrentWorkers, "concurrent-workers", 10, "How many pods to sample in parallel. This may affect the controller's stability.")
	flag.Int64Var(&minMemory, "min-memory", 0, "Minimum memory in (Mi) that the controller can set a pod request to. 0 is infinite")
	flag.Int64Var(&minCPU, "min-cpu", 0, "Minimum CPU in (m) that the controller can set a pod request to. 0 is infinite")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.IntVar(&sampleSize, "sample-size", 1, "The sample size to create an average from when reconciling.")
	flag.Float64Var(&minSecondsBetweenPodRestart, "min-seconds", 1, "Minimum seconds between pod restart. "+
		"This ensures the controller will not restart a pod if the minimum time has not passed since it has started.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableAnnotation, "annotation-filter", true,
		"Enable a annotation filter for pod scraping. "+
			"Enabling this will ensure that the controller only sets requests of controllers of which PODS or NAMESPACE have the annotation. "+
			"(reqsizer.jatalocks.github.io/optimize=true/false)")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	redisClient := redis.NewClient(&redis.Options{
		Addr:     redisHost + ":" + redisPort,
		Password: redisPassword, // no password set
		DB:       int(redisDB),  // use default DB
	})
	log.Info(redisClient)

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "d84d636a.kube-reqsizer",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		log.Error(err, err.Error())
		os.Exit(1)
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig :=
			clientcmd.NewDefaultClientConfigLoadingRules().GetDefaultFilename()
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			log.Error(err, err.Error())
		}

	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Error(err, err.Error())
	}

	if err = (&controllers.PodReconciler{
		Client:                      mgr.GetClient(),
		Log:                         ctrl.Log.WithName("controllers").WithName("Pod"),
		Scheme:                      mgr.GetScheme(),
		ClientSet:                   clientset,
		SampleSize:                  sampleSize,
		EnableAnnotation:            enableAnnotation,
		MinSecondsBetweenPodRestart: minSecondsBetweenPodRestart,
		EnableIncrease:              enableIncrease,
		EnableReduce:                enableReduce,
		MaxMemory:                   maxMemory,
		MaxCPU:                      maxCPU,
		MinMemory:                   minMemory,
		MinCPU:                      minCPU,
		MinCPUIncreasePercentage:    minCPUIncreasePercentage,
		MinMemoryIncreasePercentage: minMemoryIncreasePercentage,
		MinCPUDecreasePercentage:    minCPUDecreasePercentage,
		MinMemoryDecreasePercentage: minMemoryDecreasePercentage,
		CPUFactor:                   cpuFactor,
		MemoryFactor:                memoryFactor,
		GithubMode:                  githubMode,
		VerboseMode:                 verboseMode,
		RedisClient:                 rediscache.RedisClient{Client: redisClient},
		EnablePersistence:           enablePersistence,
	}).SetupWithManager(mgr, concurrentWorkers); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pod")
		log.Error(err, err.Error())
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		log.Error(err, err.Error())
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		log.Error(err, err.Error())
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		log.Error(err, err.Error())
		os.Exit(1)
	}
}
