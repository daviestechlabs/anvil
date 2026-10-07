package controller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	api "anvil.dev/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Helm talks to a real API server. No kubelet or Deployment controller runs here.
func TestKubernetesAPIHelmBootstrap(t *testing.T) {
	if os.Getenv("ANVIL_ENVTEST") != "1" || os.Getenv("ANVIL_HELM_TEST") != "1" {
		t.Skip("set ANVIL_ENVTEST, ANVIL_HELM_TEST, and KUBEBUILDER_ASSETS")
	}
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatal(err)
	}
	// Only Helm installs Anvil's CRDs. Loading them through envtest would hide bootstrap failures.
	environment := &envtest.Environment{}
	config, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{api.AddToScheme, corev1.AddToScheme, appsv1.AddToScheme, apiextensions.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	admin, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	create := func(object client.Object) {
		t.Helper()
		if err := admin.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	const namespace, trainingNamespace, release = "helm-app", "helm-training", "bootstrap"
	for _, name := range []string{namespace, trainingNamespace} {
		create(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}
	kubeconfig := clientcmdapi.Config{
		CurrentContext: "local",
		Clusters:       map[string]*clientcmdapi.Cluster{"local": {Server: config.Host, CertificateAuthorityData: config.CAData}},
		AuthInfos:      map[string]*clientcmdapi.AuthInfo{"admin": {ClientCertificateData: config.CertData, ClientKeyData: config.KeyData}},
		Contexts:       map[string]*clientcmdapi.Context{"local": {Cluster: "local", AuthInfo: "admin"}},
	}
	kubeconfigPath := filepath.Join(t.TempDir(), "config")
	if err := clientcmd.WriteToFile(kubeconfig, kubeconfigPath); err != nil {
		t.Fatal(err)
	}
	chart := os.Getenv("ANVIL_HELM_CHART")
	if chart == "" {
		chart = "../../charts/anvil-operator"
	}
	runHelm := func(arguments ...string) {
		t.Helper()
		commandContext, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		arguments = append([]string{"--kubeconfig", kubeconfigPath, "--namespace", namespace}, arguments...)
		output, err := exec.CommandContext(commandContext, helm, arguments...).CombinedOutput()
		if err != nil {
			t.Fatalf("Helm %v: %v\n%s", arguments, err, output)
		}
	}
	values := []string{"--set", "trainingNamespace=" + trainingNamespace, "--set", "operatorImage.digest=sha256:" + strings.Repeat("a", 64)}
	missing := append([]string{"--kubeconfig", kubeconfigPath, "--namespace", namespace, "install", release, chart, "--dry-run=server"}, values...)
	output, missingErr := exec.CommandContext(ctx, helm, missing...).CombinedOutput()
	if missingErr == nil || !strings.Contains(string(output), "Argo prerequisite missing") {
		t.Fatalf("Helm accepted missing Argo APIs: %v\n%s", missingErr, output)
	}
	if _, err := envtest.InstallCRDs(config, envtest.CRDInstallOptions{CRDs: testArgoCRDs()}); err != nil {
		t.Fatal(err)
	}
	runHelm(append([]string{"install", release, chart, "--wait=false"}, values...)...)
	definitions := &apiextensions.CustomResourceDefinitionList{}
	if err := admin.List(ctx, definitions); err != nil {
		t.Fatal(err)
	}
	if len(definitions.Items) != 9 {
		t.Fatalf("Helm did not retain three Argo and bootstrap six Anvil CRDs: %d", len(definitions.Items))
	}
	crdUIDs := map[string]types.UID{}
	for _, definition := range definitions.Items {
		crdUIDs[definition.Name] = definition.UID
	}
	operatorName := release + "-anvil-operator"
	user, err := environment.AddUser(envtest.User{Name: "system:serviceaccount:" + namespace + ":" + operatorName}, config)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := client.New(user.Config(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	install := installationFixture()
	install.Namespace = namespace
	create(install)
	create(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "database-ca", Namespace: namespace}, Data: map[string]string{"ca.crt": "fixture-public-certificate"}})
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "staging", Namespace: namespace}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: *quantity("1Gi")}}}}
	create(pvc)
	pvc.Status.Phase = corev1.ClaimBound
	if err := admin.Status().Update(ctx, pvc); err != nil {
		t.Fatal(err)
	}
	// Allow the real API server's authorization cache to observe Helm's RoleBindings.
	if err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		return operator.Get(ctx, client.ObjectKeyFromObject(install), &api.Anvil{}) == nil, nil
	}); err != nil {
		t.Fatal("Helm's operator cannot read its installation", err)
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(user.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckArgoAPI(discoveryClient); err != nil {
		t.Fatal("operator's scoped account cannot discover Argo prerequisites", err)
	}
	reconciler := &InstallationReconciler{Client: operator, Reader: operator, Scheme: scheme, Namespace: namespace}
	for i := 0; i < 2; i++ {
		if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(install)}); err != nil {
			t.Fatal("Helm's actual operator permissions cannot reconcile", err)
		}
	}
	web := &appsv1.Deployment{}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(install), web); err != nil {
		t.Fatal(err)
	}
	if web.Spec.Template.Spec.Containers[0].Image != install.Spec.Image || len(web.OwnerReferences) != 1 || web.OwnerReferences[0].UID != install.UID {
		t.Fatal("Helm operator did not create the bound application")
	}
	for _, name := range []string{namespace, trainingNamespace, "default"} {
		if err := operator.Get(ctx, types.NamespacedName{Namespace: name, Name: "credentials"}, &corev1.Secret{}); !apierrors.IsForbidden(err) {
			t.Fatalf("operator can read Secrets in %s: %v", name, err)
		}
	}
	if err := operator.Get(ctx, types.NamespacedName{Namespace: "default", Name: "other-app"}, &appsv1.Deployment{}); !apierrors.IsForbidden(err) {
		t.Fatalf("operator can inspect another application's namespace: %v", err)
	}
	values[len(values)-1] = "operatorImage.digest=sha256:" + strings.Repeat("b", 64)
	runHelm(append([]string{"upgrade", release, chart, "--wait=false"}, values...)...)
	operatorDeployment := &appsv1.Deployment{}
	if err := admin.Get(ctx, types.NamespacedName{Namespace: namespace, Name: operatorName}, operatorDeployment); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(operatorDeployment.Spec.Template.Spec.Containers[0].Image, strings.Repeat("b", 64)) {
		t.Fatal("Helm upgrade did not update the operator image")
	}
	runHelm("uninstall", release, "--wait=false")
	if err := admin.Get(ctx, types.NamespacedName{Namespace: namespace, Name: operatorName}, operatorDeployment); !apierrors.IsNotFound(err) {
		t.Fatalf("Helm uninstall retained its operator Deployment: %v", err)
	}
	for name, uid := range crdUIDs {
		definition := &apiextensions.CustomResourceDefinition{}
		if err := admin.Get(ctx, types.NamespacedName{Name: name}, definition); err != nil || definition.UID != uid {
			t.Fatalf("Helm upgrade or removal replaced or removed CRD %s: %v", name, err)
		}
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(install), &api.Anvil{}); err != nil {
		t.Fatal("Helm removal deleted the separately managed installation", err)
	}
	retained := &corev1.PersistentVolumeClaim{}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(pvc), retained); err != nil || retained.UID != pvc.UID || len(retained.OwnerReferences) != 0 {
		t.Fatalf("Helm modified external storage ownership: %v", err)
	}
}
