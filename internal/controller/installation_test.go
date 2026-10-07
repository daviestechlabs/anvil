package controller

import (
	"context"
	"os"
	"strings"
	"testing"

	api "anvil.dev/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func installationFixture() *api.Anvil {
	return &api.Anvil{ObjectMeta: metav1.ObjectMeta{Name: "anvil", Namespace: "anvil-test"}, Spec: api.AnvilSpec{
		Image: "registry.example/anvil@sha256:" + strings.Repeat("a", 64), PublicOrigin: "https://anvil.example",
		Identity:          api.ConsoleIdentity{Issuer: "https://identity.example/", Audience: "anvil", JWKSURL: "https://identity.example/jwks", RequiredGroup: "operators", TokenHeader: "x-envoy-oidc-id-token"},
		DatabaseSecretRef: api.KeyReference{Name: "database", Key: "uri"}, DatabaseCARef: api.CertificateReference{Name: "database-ca", Key: "ca.crt"}, StagingClaim: "staging", WebReplicas: 1, ControllerEnabled: true, ConfigurationRevision: "1",
	}}
}

func TestKubernetesAPIInstallation(t *testing.T) {
	if os.Getenv("ANVIL_ENVTEST") != "1" {
		t.Skip("set ANVIL_ENVTEST and KUBEBUILDER_ASSETS")
	}
	preserve := true
	routeCRD := &apiextensions.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "httproutes.gateway.networking.k8s.io", Annotations: map[string]string{"api-approved.kubernetes.io": "unapproved, experimental-only test fixture"}}, Spec: apiextensions.CustomResourceDefinitionSpec{Group: HTTPRouteGVK.Group, Scope: apiextensions.NamespaceScoped, Names: apiextensions.CustomResourceDefinitionNames{Plural: "httproutes", Kind: "HTTPRoute"}, Versions: []apiextensions.CustomResourceDefinitionVersion{{Name: "v1", Served: true, Storage: true, Schema: &apiextensions.CustomResourceValidation{OpenAPIV3Schema: &apiextensions.JSONSchemaProps{Type: "object", Properties: map[string]apiextensions.JSONSchemaProps{"spec": {Type: "object", XPreserveUnknownFields: &preserve}, "status": {Type: "object", XPreserveUnknownFields: &preserve}}}}, Subresources: &apiextensions.CustomResourceSubresources{Status: &apiextensions.CustomResourceSubresourceStatus{}}}}}}
	environment := &envtest.Environment{CRDDirectoryPaths: []string{"../../config/crd"}, CRDs: []*apiextensions.CustomResourceDefinition{routeCRD}, ErrorIfCRDPathMissing: true}
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
	for _, add := range []func(*runtime.Scheme) error{api.AddToScheme, corev1.AddToScheme, appsv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	create := func(object client.Object) {
		t.Helper()
		if err := c.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	create(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "anvil-test"}})
	install := installationFixture()
	rejected := install.DeepCopy()
	rejected.Name = "other"
	if err := c.Create(ctx, rejected); !apierrors.IsInvalid(err) {
		t.Fatalf("non-singleton was admitted: %v", err)
	}
	rejected = install.DeepCopy()
	rejected.Spec.Image = "registry.example/anvil:latest"
	if err := c.Create(ctx, rejected); !apierrors.IsInvalid(err) {
		t.Fatalf("mutable image was admitted: %v", err)
	}
	create(install)
	r := &InstallationReconciler{Client: c, Reader: c, Scheme: scheme, Namespace: install.Namespace}
	step := func() {
		t.Helper()
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(install)}); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(install), install); err != nil {
			t.Fatal(err)
		}
	}
	step()
	if condition := meta.FindStatusCondition(install.Status.Conditions, "Ready"); condition == nil || condition.Reason != "DependencyMissing" {
		t.Fatal("missing dependency was not held")
	}
	ca := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "database-ca", Namespace: install.Namespace}, Data: map[string]string{"ca.crt": "fixture-public-certificate"}}
	create(ca)
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "staging", Namespace: install.Namespace}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: *quantity("1Gi")}}}}
	create(pvc)
	pvc.Status.Phase = corev1.ClaimBound
	if err := c.Status().Update(ctx, pvc); err != nil {
		t.Fatal(err)
	}
	rejected = install.DeepCopy()
	rejected.Spec.StagingClaim = "different"
	if err := c.Update(ctx, rejected); !apierrors.IsInvalid(err) {
		t.Fatalf("storage replacement admitted: %v", err)
	}
	// An existing Git-owned resource blocks the whole installation before partial creation.
	conflict := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "anvil", Namespace: install.Namespace}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}}}
	create(conflict)
	step()
	if condition := meta.FindStatusCondition(install.Status.Conditions, "Ready"); condition.Reason != "OwnershipConflict" {
		t.Fatal("existing resource was adopted")
	}
	missing := &corev1.ServiceAccount{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: install.Namespace, Name: "anvil-web"}, missing); !apierrors.IsNotFound(err) {
		t.Fatal("ownership conflict partially installed resources")
	}
	if err := c.Delete(ctx, conflict); err != nil {
		t.Fatal(err)
	}
	step()
	step() // Real server-side apply must remain idempotent after creation.
	deployments := &appsv1.DeploymentList{}
	if err := c.List(ctx, deployments, client.InNamespace(install.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(deployments.Items) != 3 {
		t.Fatal("application components are missing")
	}
	markReady := func() {
		t.Helper()
		if err := c.List(ctx, deployments, client.InNamespace(install.Namespace)); err != nil {
			t.Fatal(err)
		}
		for i := range deployments.Items {
			d := &deployments.Items[i]
			d.Status.ObservedGeneration = d.Generation
			d.Status.Replicas = *d.Spec.Replicas
			d.Status.UpdatedReplicas = *d.Spec.Replicas
			d.Status.AvailableReplicas = *d.Spec.Replicas
			d.Status.ReadyReplicas = *d.Spec.Replicas
			if err := c.Status().Update(ctx, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	markReady()
	step()
	if !meta.IsStatusConditionTrue(install.Status.Conditions, "Ready") {
		t.Fatal("current healthy Deployments did not become ready")
	}
	web := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(install), web); err != nil {
		t.Fatal(err)
	}
	firstDigest := web.Spec.Template.Annotations["anvil.dev/configuration-sha256"]
	ca.Data["ca.crt"] = "rotated-public-certificate"
	if err := c.Update(ctx, ca); err != nil {
		t.Fatal(err)
	}
	step()
	if err := c.Get(ctx, client.ObjectKeyFromObject(install), web); err != nil {
		t.Fatal(err)
	}
	if web.Spec.Template.Annotations["anvil.dev/configuration-sha256"] == firstDigest || meta.IsStatusConditionTrue(install.Status.Conditions, "Ready") {
		t.Fatal("configuration change did not roll out or used stale readiness")
	}
	install.Spec.Image = "registry.example/anvil@sha256:" + strings.Repeat("b", 64)
	install.Spec.WebReplicas = 2
	install.Spec.Gateway = &api.GatewayReference{Name: "public", Namespace: "gateway", SectionName: "https"}
	if err := c.Update(ctx, install); err != nil {
		t.Fatal(err)
	}
	step()
	markReady()
	step()
	if err := c.Get(ctx, client.ObjectKeyFromObject(install), web); err != nil {
		t.Fatal(err)
	}
	if web.Spec.Template.Spec.Containers[0].Image != install.Spec.Image || *web.Spec.Replicas != 2 {
		t.Fatal("image or scaling update was not applied")
	}
	if condition := meta.FindStatusCondition(install.Status.Conditions, "Ready"); condition.Reason != "GatewayPending" {
		t.Fatal("unaccepted Gateway was reported ready")
	}
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(HTTPRouteGVK)
	if err := c.Get(ctx, client.ObjectKeyFromObject(install), route); err != nil {
		t.Fatal(err)
	}
	route.Object["status"] = map[string]any{"parents": []any{map[string]any{"parentRef": map[string]any{"name": "public", "namespace": "gateway", "sectionName": "https"}, "conditions": []any{map[string]any{"type": "Accepted", "status": "True", "observedGeneration": route.GetGeneration()}, map[string]any{"type": "ResolvedRefs", "status": "True", "observedGeneration": route.GetGeneration()}}}}}
	if err := c.Status().Update(ctx, route); err != nil {
		t.Fatal(err)
	}
	step()
	if !meta.IsStatusConditionTrue(install.Status.Conditions, "Ready") || install.Status.ReadyReplicas != 2 {
		t.Fatal("Gateway-backed updated installation was not ready")
	}
	install.Spec.Gateway = nil
	if err := c.Update(ctx, install); err != nil {
		t.Fatal(err)
	}
	step()
	if err := c.Get(ctx, client.ObjectKeyFromObject(install), route); !apierrors.IsNotFound(err) {
		t.Fatal("disabled route was retained")
	}
	// A replaced child cannot be modified using the identity recorded during preflight.
	stale := web.DeepCopy()
	if err := c.Get(ctx, client.ObjectKeyFromObject(install), stale); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, stale); err != nil {
		t.Fatal(err)
	}
	replacement := stale.DeepCopy()
	replacement.SetUID("")
	replacement.SetResourceVersion("")
	replacement.SetManagedFields(nil)
	replacement.SetOwnerReferences(nil)
	create(replacement)
	stale.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}
	stale.SetManagedFields(nil)
	if err := c.Patch(ctx, stale, client.Apply, client.FieldOwner("anvil-installation"), client.ForceOwnership); !apierrors.IsConflict(err) {
		t.Fatalf("stale child identity was not rejected: %v", err)
	}
	step()
	if condition := meta.FindStatusCondition(install.Status.Conditions, "Ready"); condition.Reason != "OwnershipConflict" {
		t.Fatal("replacement child was adopted")
	}
	if err := c.Delete(ctx, install); err != nil {
		t.Fatal(err)
	}
	// Envtest lacks garbage collection; ownership proves app cleanup, while storage remains external.
	if err := c.Get(ctx, client.ObjectKeyFromObject(pvc), pvc); err != nil {
		t.Fatal("installation deletion removed storage")
	}
	if len(pvc.OwnerReferences) != 0 {
		t.Fatal("installation owns data storage")
	}
}

func TestInstallationCertificateAndWorkerIsolation(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = api.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	a := installationFixture()
	a.UID = "installation"
	a.Spec.DatabaseCARef.Kind = "Secret"
	objects, err := installationObjects(a, nil, "configuration", scheme)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		owner := metav1.GetControllerOf(object)
		if owner == nil || owner.UID != a.UID {
			t.Fatal("application child lacks its owner")
		}
		d, ok := object.(*appsv1.Deployment)
		if !ok {
			continue
		}
		pod := d.Spec.Template.Spec
		if *pod.AutomountServiceAccountToken || !*pod.SecurityContext.RunAsNonRoot {
			t.Fatal("unsafe application identity")
		}
		if pod.Volumes[2].Secret == nil || pod.Volumes[2].Secret.SecretName != a.Spec.DatabaseCARef.Name {
			t.Fatal("CNPG certificate reference was not mounted")
		}
		credentials := 0
		for _, v := range pod.Volumes {
			if v.Projected != nil {
				credentials++
			}
		}
		if d.Name == "anvil-controller" {
			if credentials != 1 || pod.Volumes[3].Projected.Sources[0].ServiceAccountToken == nil || *pod.Volumes[3].Projected.Sources[0].ServiceAccountToken.ExpirationSeconds != 600 {
				t.Fatal("controller token is not bounded")
			}
			if pod.Containers[0].Env[0].Value != "127.0.0.1" {
				t.Fatal("controller listener is public")
			}
		} else if credentials != 0 {
			t.Fatal("non-controller workload received an API token")
		}
	}
}
