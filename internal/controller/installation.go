package controller

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"time"

	api "anvil.dev/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var HTTPRouteGVK = schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"}

type InstallationReconciler struct {
	client.Client
	Reader    client.Reader
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Namespace string
}

func (r *InstallationReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).For(&api.Anvil{}).
		Owns(&appsv1.Deployment{}).Owns(&corev1.Service{}).Owns(&corev1.ServiceAccount{}).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.dependencyChanged)).
		Watches(&corev1.PersistentVolumeClaim{}, handler.EnqueueRequestsFromMapFunc(r.dependencyChanged)).
		Complete(r)
}

func (r *InstallationReconciler) dependencyChanged(_ context.Context, object client.Object) []reconcile.Request {
	if object.GetNamespace() != r.Namespace {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: r.Namespace, Name: "anvil"}}}
}

func (r *InstallationReconciler) condition(ctx context.Context, installation *api.Anvil, ready bool, reason, message string, digest string, replicas int32) (ctrl.Result, error) {
	before := installation.DeepCopy()
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	installation.Status.ObservedGeneration = installation.Generation
	installation.Status.ConfigurationSHA256 = digest
	installation.Status.ReadyReplicas = replicas
	meta.SetStatusCondition(&installation.Status.Conditions, metav1.Condition{Type: "Ready", Status: status, Reason: reason, Message: message, ObservedGeneration: installation.Generation})
	if !reflect.DeepEqual(before.Status, installation.Status) {
		if err := r.Status().Patch(ctx, installation, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
		if r.Recorder != nil {
			eventType := corev1.EventTypeNormal
			if !ready {
				eventType = corev1.EventTypeWarning
			}
			r.Recorder.Event(installation, eventType, reason, message)
		}
	}
	delay := 5 * time.Second
	if ready {
		delay = 30 * time.Second
	}
	return ctrl.Result{RequeueAfter: delay}, nil
}

func (r *InstallationReconciler) configuration(ctx context.Context, installation *api.Anvil) (map[string]*corev1.ConfigMap, error) {
	maps := map[string]*corev1.ConfigMap{}
	names := []string{installation.Spec.FleetConfigMap, installation.Spec.TrainingConfigMap}
	if installation.Spec.DatabaseCARef.Kind != "Secret" {
		names = append(names, installation.Spec.DatabaseCARef.Name)
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		cm := &corev1.ConfigMap{}
		if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: installation.Namespace, Name: name}, cm); err != nil {
			return nil, fmt.Errorf("ConfigMap %s is unavailable: %w", name, err)
		}
		maps[name] = cm
	}
	ca := maps[installation.Spec.DatabaseCARef.Name]
	if installation.Spec.DatabaseCARef.Kind != "Secret" && (ca == nil || ca.Data[installation.Spec.DatabaseCARef.Key] == "") {
		return nil, fmt.Errorf("database CA ConfigMap key is missing")
	}
	return maps, nil
}

func (r *InstallationReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	if request.Namespace != r.Namespace {
		return ctrl.Result{}, nil
	}
	installation := &api.Anvil{}
	if err := r.Reader.Get(ctx, request.NamespacedName, installation); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !installation.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if installation.Name != "anvil" {
		return r.condition(ctx, installation, false, "InvalidInstallation", "The installation must be named anvil", "", 0)
	}
	maps, err := r.configuration(ctx, installation)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return r.condition(ctx, installation, false, "DependencyMissing", "A referenced configuration object is missing", "", 0)
		}
		return r.condition(ctx, installation, false, "ConfigurationUnavailable", "Configuration could not be verified", "", 0)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: installation.Namespace, Name: installation.Spec.StagingClaim}, pvc); err != nil {
		if apierrors.IsNotFound(err) {
			return r.condition(ctx, installation, false, "DependencyMissing", "The existing staging claim is missing", "", 0)
		}
		return ctrl.Result{}, err
	}
	shared := false
	for _, mode := range pvc.Spec.AccessModes {
		if mode == corev1.ReadWriteMany {
			shared = true
		}
	}
	if !shared || pvc.Status.Phase != corev1.ClaimBound {
		return r.condition(ctx, installation, false, "StoragePending", "Staging needs a Bound ReadWriteMany claim", "", 0)
	}
	configuration := map[string]any{}
	for name, cm := range maps {
		configuration[name] = cm.Data
	}
	contents := map[string]any{"spec": installation.Spec, "configuration": configuration}
	digest, err := Hash(contents)
	if err != nil {
		return ctrl.Result{}, err
	}
	desired, err := installationObjects(installation, maps, digest, r.Scheme)
	if err != nil {
		return r.condition(ctx, installation, false, "InvalidInstallation", "Installation configuration is invalid", digest, 0)
	}
	// Check the complete set before any apply. Never adopt the live Flux deployment.
	existing := map[client.Object]bool{}
	for _, object := range desired {
		current := object.DeepCopyObject().(client.Object)
		err := r.Reader.Get(ctx, client.ObjectKeyFromObject(object), current)
		if apierrors.IsNotFound(err) {
			continue
		}
		if meta.IsNoMatchError(err) {
			return r.condition(ctx, installation, false, "DependencyMissing", "Install Gateway API before enabling an HTTPRoute", digest, 0)
		}
		if err != nil {
			return ctrl.Result{}, err
		}
		owner := metav1.GetControllerOf(current)
		if owner == nil || owner.UID != installation.UID || owner.Name != installation.Name || owner.Kind != "Anvil" || owner.APIVersion != api.GroupVersion.String() {
			return r.condition(ctx, installation, false, "OwnershipConflict", fmt.Sprintf("%s %s already has another owner", object.GetObjectKind().GroupVersionKind().Kind, object.GetName()), digest, 0)
		}
		object.SetUID(current.GetUID())
		object.SetResourceVersion(current.GetResourceVersion())
		existing[object] = true
	}
	for _, object := range desired {
		if !existing[object] {
			if err := r.Create(ctx, object); err != nil {
				return ctrl.Result{}, err
			}
			continue
		}
		// Ownership was verified above. Anvil controls only the rendered fields on its own children.
		if err := r.Patch(ctx, object, client.Apply, client.FieldOwner("anvil-installation"), client.ForceOwnership); err != nil {
			return ctrl.Result{}, err
		}
	}
	if installation.Spec.Gateway == nil {
		route := &unstructured.Unstructured{}
		route.SetGroupVersionKind(HTTPRouteGVK)
		err := r.Reader.Get(ctx, request.NamespacedName, route)
		if err == nil {
			owner := metav1.GetControllerOf(route)
			if owner != nil && owner.UID == installation.UID && owner.Kind == "Anvil" && owner.APIVersion == api.GroupVersion.String() {
				uid, version := route.GetUID(), route.GetResourceVersion()
				if err := r.Delete(ctx, route, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &version}}); err != nil {
					return ctrl.Result{}, err
				}
			}
		} else if !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
			return ctrl.Result{}, err
		}
	}
	ready, replicas := true, int32(0)
	for _, object := range desired {
		if deployment, ok := object.(*appsv1.Deployment); ok {
			actual := &appsv1.Deployment{}
			if err := r.Reader.Get(ctx, client.ObjectKeyFromObject(deployment), actual); err != nil {
				return ctrl.Result{}, err
			}
			wanted := *deployment.Spec.Replicas
			if actual.Status.ObservedGeneration < actual.Generation || actual.Status.UpdatedReplicas != wanted || actual.Status.AvailableReplicas != wanted {
				ready = false
			}
			if deployment.Name == "anvil" {
				replicas = actual.Status.ReadyReplicas
			}
		}
	}
	if !ready {
		return r.condition(ctx, installation, false, "Progressing", "Waiting for the current application Deployments", digest, replicas)
	}
	if installation.Spec.Gateway != nil {
		route := &unstructured.Unstructured{}
		route.SetGroupVersionKind(HTTPRouteGVK)
		if err := r.Reader.Get(ctx, request.NamespacedName, route); err != nil {
			return ctrl.Result{}, err
		}
		if !routeReady(route, installation.Spec.Gateway) {
			return r.condition(ctx, installation, false, "GatewayPending", "Waiting for Gateway acceptance and resolved references", digest, replicas)
		}
	}
	return r.condition(ctx, installation, true, "Available", "The requested application generation is available", digest, replicas)
}

func routeReady(route *unstructured.Unstructured, gateway *api.GatewayReference) bool {
	parents, _, _ := unstructured.NestedSlice(route.Object, "status", "parents")
	for _, raw := range parents {
		parent, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		reference, ok := parent["parentRef"].(map[string]any)
		if !ok {
			continue
		}
		namespace, _ := reference["namespace"].(string)
		if namespace == "" {
			namespace = route.GetNamespace()
		}
		if reference["name"] != gateway.Name || namespace != gateway.Namespace || reference["sectionName"] != gateway.SectionName {
			continue
		}
		conditions, _ := parent["conditions"].([]any)
		accepted, resolved := false, false
		for _, rawCondition := range conditions {
			c, ok := rawCondition.(map[string]any)
			if !ok {
				continue
			}
			generation, ok := c["observedGeneration"].(int64)
			if !ok || generation != route.GetGeneration() || c["status"] != "True" {
				continue
			}
			if c["type"] == "Accepted" {
				accepted = true
			}
			if c["type"] == "ResolvedRefs" {
				resolved = true
			}
		}
		if accepted && resolved {
			return true
		}
	}
	return false
}

func installationObjects(a *api.Anvil, maps map[string]*corev1.ConfigMap, digest string, scheme *runtime.Scheme) ([]client.Object, error) {
	origin, err := url.Parse(a.Spec.PublicOrigin)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.Path != "" {
		return nil, fmt.Errorf("invalid public origin")
	}
	objects := []client.Object{}
	for _, name := range []string{"anvil-web", "anvil-controller"} {
		objects = append(objects, &corev1.ServiceAccount{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"}, ObjectMeta: metav1.ObjectMeta{Name: name}, AutomountServiceAccountToken: ptr(false)})
	}
	for _, component := range []string{"web", "controller", "artifact-importer"} {
		replicas := int32(1)
		name := "anvil-" + component
		if component == "web" {
			name = "anvil"
			replicas = a.Spec.WebReplicas
		}
		if component == "controller" && !a.Spec.ControllerEnabled {
			replicas = 0
		}
		if component == "artifact-importer" && !a.Spec.ArtifactImporterEnabled {
			replicas = 0
		}
		env := []corev1.EnvVar{
			{Name: "HOST", Value: "0.0.0.0"}, {Name: "PORT", Value: "8080"}, {Name: "ANVIL_PROCESS_ROLE", Value: component},
			{Name: "ANVIL_REQUIRE_POSTGRES", Value: "true"}, {Name: "ANVIL_REQUIRE_DATABASE_TLS", Value: "true"},
			{Name: "ANVIL_DATABASE_TLS_CA_FILE", Value: "/etc/anvil/database/ca.crt"},
			{Name: "DATABASE_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: a.Spec.DatabaseSecretRef.Name}, Key: a.Spec.DatabaseSecretRef.Key}}},
			{Name: "ANVIL_STAGING_ROOT", Value: "/var/lib/anvil/staging"}, {Name: "ANVIL_KUBERNETES_NAMESPACE", Value: a.Namespace},
		}
		readiness, liveness := httpProbe(8), httpProbe(15)
		if component != "web" {
			env[0].Value = "127.0.0.1"
			readiness, liveness = execProbe(8), execProbe(15)
		}
		bindings := map[string]string{"targets.json": "ANVIL_TARGETS_JSON", "runtime-images.json": "ANVIL_RUNTIME_IMAGES_JSON", "vllm-candidate-images.json": "ANVIL_VLLM_CANDIDATE_IMAGES_JSON", "native-evaluations.json": "ANVIL_NATIVE_EVALUATIONS_JSON", "governed-bundle-targets.json": "ANVIL_GOVERNED_BUNDLE_TARGETS_JSON", "forge-recipes.json": "ANVIL_FORGE_RECIPES_JSON", "experiment-candidates.json": "ANVIL_EXPERIMENT_CANDIDATES_JSON"}
		keys := []string{}
		for key := range bindings {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if cm := maps[a.Spec.FleetConfigMap]; cm != nil {
			for _, key := range keys {
				if _, exists := cm.Data[key]; exists {
					env = append(env, configEnv(bindings[key], cm.Name, key))
				}
			}
		}
		if cm := maps[a.Spec.TrainingConfigMap]; cm != nil {
			env = append(env, configEnv("ANVIL_TRAINING_CATALOG_JSON", cm.Name, "catalog.json"))
		}
		if component == "web" {
			env = append(env, corev1.EnvVar{Name: "ANVIL_GATEWAY_IDENTITY", Value: "true"}, corev1.EnvVar{Name: "ANVIL_PUBLIC_ORIGIN", Value: a.Spec.PublicOrigin}, corev1.EnvVar{Name: "ANVIL_OIDC_ISSUER", Value: a.Spec.Identity.Issuer}, corev1.EnvVar{Name: "ANVIL_OIDC_AUDIENCE", Value: a.Spec.Identity.Audience}, corev1.EnvVar{Name: "ANVIL_OIDC_JWKS_URL", Value: a.Spec.Identity.JWKSURL}, corev1.EnvVar{Name: "ANVIL_REQUIRED_GROUP", Value: a.Spec.Identity.RequiredGroup}, corev1.EnvVar{Name: "ANVIL_ID_TOKEN_HEADER", Value: a.Spec.Identity.TokenHeader})
		}
		volumes := []corev1.Volume{
			{Name: "staging", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: a.Spec.StagingClaim}}},
			{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: quantity("128Mi")}}},
			{Name: "database-ca", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: a.Spec.DatabaseCARef.Name}, Items: []corev1.KeyToPath{{Key: a.Spec.DatabaseCARef.Key, Path: "ca.crt"}}}}},
		}
		if a.Spec.DatabaseCARef.Kind == "Secret" {
			volumes[2].VolumeSource = corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: a.Spec.DatabaseCARef.Name, Items: []corev1.KeyToPath{{Key: a.Spec.DatabaseCARef.Key, Path: "ca.crt"}}}}
		}
		mounts := []corev1.VolumeMount{{Name: "staging", MountPath: "/var/lib/anvil/staging"}, {Name: "tmp", MountPath: "/tmp"}, {Name: "database-ca", MountPath: "/etc/anvil/database", ReadOnly: true}}
		sa := "anvil-web"
		if component == "controller" {
			sa = "anvil-controller"
			env = append(env, corev1.EnvVar{Name: "NODE_EXTRA_CA_CERTS", Value: "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"})
			volumes = append(volumes, corev1.Volume{Name: "kubernetes-api", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{
				{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{ExpirationSeconds: ptr(int64(600)), Path: "token"}},
				{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "kube-root-ca.crt"}, Items: []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}}}},
				{DownwardAPI: &corev1.DownwardAPIProjection{Items: []corev1.DownwardAPIVolumeFile{{Path: "namespace", FieldRef: &corev1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "metadata.namespace"}}}}},
			}}}})
			mounts = append(mounts, corev1.VolumeMount{Name: "kubernetes-api", MountPath: "/var/run/secrets/kubernetes.io/serviceaccount", ReadOnly: true})
		}
		labels := map[string]string{"app.kubernetes.io/name": "anvil", "app.kubernetes.io/component": component}
		deployment := &appsv1.Deployment{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: labels},
				Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: map[string]string{"anvil.dev/configuration-sha256": digest}},
					Spec: corev1.PodSpec{
						ServiceAccountName: sa, AutomountServiceAccountToken: ptr(false),
						SecurityContext: &corev1.PodSecurityContext{
							RunAsNonRoot: ptr(true), RunAsUser: ptr(int64(100)), RunAsGroup: ptr(int64(101)), FSGroup: ptr(int64(101)),
							SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						},
						Containers: []corev1.Container{{
							Name: component, Image: a.Spec.Image, Env: env,
							Ports:        []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
							VolumeMounts: mounts,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr(false), ReadOnlyRootFilesystem: ptr(true),
								Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{corev1.ResourceCPU: *quantity("50m"), corev1.ResourceMemory: *quantity("256Mi")},
								Limits:   corev1.ResourceList{corev1.ResourceCPU: *quantity("1"), corev1.ResourceMemory: *quantity("1Gi")},
							},
							ReadinessProbe: readiness, LivenessProbe: liveness,
						}},
						Volumes: volumes,
					},
				},
			},
		}
		if component == "web" {
			deployment.Spec.Strategy = appsv1.DeploymentStrategy{
				Type:          appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{MaxSurge: ptr(intstrPort(1)), MaxUnavailable: ptr(intstrPort(0))},
			}
		}
		objects = append(objects, deployment)
	}
	objects = append(objects, &corev1.Service{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Service"}, ObjectMeta: metav1.ObjectMeta{Name: "anvil"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, Selector: map[string]string{"app.kubernetes.io/name": "anvil", "app.kubernetes.io/component": "web"}, Ports: []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstrPort(8080)}}}})
	if a.Spec.Gateway != nil {
		gateway := a.Spec.Gateway
		route := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"hostnames": []any{origin.Hostname()}, "parentRefs": []any{map[string]any{"name": gateway.Name, "namespace": gateway.Namespace, "sectionName": gateway.SectionName}}, "rules": []any{map[string]any{"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/"}}}, "backendRefs": []any{map[string]any{"name": "anvil", "port": int64(80)}}}}}}}
		route.SetGroupVersionKind(HTTPRouteGVK)
		route.SetName("anvil")
		objects = append(objects, route)
	}
	for _, object := range objects {
		object.SetNamespace(a.Namespace)
		object.SetLabels(map[string]string{"app.kubernetes.io/managed-by": "anvil-operator", "anvil.dev/installation-uid": string(a.UID)})
		if err := ctrl.SetControllerReference(a, object, scheme); err != nil {
			return nil, err
		}
	}
	return objects, nil
}

func configEnv(env, name, key string) corev1.EnvVar {
	return corev1.EnvVar{Name: env, ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}}}
}

func ptr[T any](value T) *T                    { return &value }
func quantity(value string) *resource.Quantity { parsed := resource.MustParse(value); return &parsed }
func intstrPort(port int32) intstr.IntOrString { return intstr.FromInt32(port) }

func httpProbe(delay int32) *corev1.Probe {
	return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/", Port: intstrPort(8080)}}, InitialDelaySeconds: delay, PeriodSeconds: 10}
}
func execProbe(delay int32) *corev1.Probe {
	return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"node", "-e", "fetch('http://127.0.0.1:8080/').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"}}}, InitialDelaySeconds: delay, PeriodSeconds: 10}
}
