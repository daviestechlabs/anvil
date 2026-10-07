package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	api "anvil.dev/operator/api/v1alpha1"
	"anvil.dev/operator/internal/controller"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metrics "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func main() {
	namespace := flag.String("namespace", "anvil-training", "Single namespace containing recipes, runs, templates and workers")
	installationNamespace := flag.String("installation-namespace", "", "Application namespace; empty disables installation reconciliation")
	leader := flag.Bool("leader-elect", true, "Use a Kubernetes Lease to permit one active controller")
	metricsAddress := flag.String("metrics-bind-address", "0", "Metrics listener; 0 disables metrics")
	flag.Parse()
	ctrl.SetLogger(zap.New())
	if issues := validation.IsDNS1123Label(*namespace); len(issues) > 0 {
		must(fmt.Errorf("invalid operator namespace: %v", issues))
	}
	if *installationNamespace != "" {
		if issues := validation.IsDNS1123Label(*installationNamespace); len(issues) > 0 {
			must(fmt.Errorf("invalid installation namespace: %v", issues))
		}
	}
	scheme := runtime.NewScheme()
	must(corev1.AddToScheme(scheme))
	must(appsv1.AddToScheme(scheme))
	must(api.AddToScheme(scheme))
	namespaces := map[string]cache.Config{*namespace: {}}
	trainingCache := map[string]cache.Config{*namespace: {}}
	workflow := &unstructured.Unstructured{}
	workflow.SetGroupVersionKind(controller.WorkflowGVK)
	byObject := map[client.Object]cache.ByObject{&api.TrainingRun{}: {Namespaces: trainingCache}, workflow: {Namespaces: trainingCache}}
	for _, object := range []client.Object{&api.TrainingArtifact{}, &api.EvaluationRun{}, &api.ModelPromotion{}} {
		byObject[object] = cache.ByObject{Namespaces: trainingCache}
	}
	if *installationNamespace != "" {
		namespaces[*installationNamespace] = cache.Config{}
		applicationCache := map[string]cache.Config{*installationNamespace: {}}
		for _, object := range []client.Object{&api.Anvil{}, &appsv1.Deployment{}, &corev1.Service{}, &corev1.ServiceAccount{}, &corev1.ConfigMap{}, &corev1.PersistentVolumeClaim{}} {
			byObject[object] = cache.ByObject{Namespaces: applicationCache}
		}
	}
	config := ctrl.GetConfigOrDie()
	checkConfig := rest.CopyConfig(config)
	checkConfig.Timeout = 10 * time.Second
	apiDiscovery, err := discovery.NewDiscoveryClientForConfig(checkConfig)
	must(err)
	must(controller.CheckArgoAPI(apiDiscovery))
	manager, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		Cache:                  cache.Options{DefaultNamespaces: namespaces, ByObject: byObject},
		Metrics:                metrics.Options{BindAddress: *metricsAddress},
		HealthProbeBindAddress: ":8081",
		LeaderElection:         *leader, LeaderElectionID: "anvil-operator.anvil.dev", LeaderElectionNamespace: *namespace,
	})
	must(err)
	reconciler := &controller.Reconciler{Client: manager.GetClient(), Reader: manager.GetAPIReader(), Recorder: manager.GetEventRecorderFor("anvil-training")}
	must(reconciler.SetupWithManager(manager))
	for _, kind := range []string{"TrainingArtifact", "EvaluationRun", "ModelPromotion"} {
		must((&controller.EvidenceReconciler{Client: manager.GetClient(), Reader: manager.GetAPIReader(), Kind: kind}).SetupWithManager(manager))
	}
	if *metricsAddress != "0" {
		must(manager.Add(&controller.TrainingMetrics{Reader: manager.GetAPIReader(), Namespace: *namespace}))
	}
	if *installationNamespace != "" {
		installer := &controller.InstallationReconciler{Client: manager.GetClient(), Reader: manager.GetAPIReader(), Scheme: scheme, Recorder: manager.GetEventRecorderFor("anvil-installation"), Namespace: *installationNamespace}
		must(installer.SetupWithManager(manager))
	}
	must(manager.AddHealthzCheck("healthz", healthz.Ping))
	must(manager.AddReadyzCheck("readyz", func(request *http.Request) error {
		if !manager.GetCache().WaitForCacheSync(request.Context()) {
			return fmt.Errorf("controller caches are not synchronized")
		}
		return nil
	}))
	must(manager.Start(ctrl.SetupSignalHandler()))
}
func must(err error) {
	if err != nil {
		ctrl.Log.Error(err, "operator failed")
		os.Exit(1)
	}
}
