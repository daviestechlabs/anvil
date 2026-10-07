package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type KeyReference struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type CertificateReference struct {
	Name string `json:"name"`
	Key  string `json:"key"`
	Kind string `json:"kind,omitempty"`
}

type GatewayReference struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	SectionName string `json:"sectionName"`
}

type ConsoleIdentity struct {
	Issuer        string `json:"issuer"`
	Audience      string `json:"audience"`
	JWKSURL       string `json:"jwksURL"`
	RequiredGroup string `json:"requiredGroup"`
	TokenHeader   string `json:"tokenHeader"`
}

type AnvilSpec struct {
	Image                   string               `json:"image"`
	PublicOrigin            string               `json:"publicOrigin"`
	Identity                ConsoleIdentity      `json:"identity"`
	DatabaseSecretRef       KeyReference         `json:"databaseSecretRef"`
	DatabaseCARef           CertificateReference `json:"databaseCARef"`
	StagingClaim            string               `json:"stagingClaim"`
	FleetConfigMap          string               `json:"fleetConfigMap,omitempty"`
	TrainingConfigMap       string               `json:"trainingConfigMap,omitempty"`
	ConfigurationRevision   string               `json:"configurationRevision"`
	WebReplicas             int32                `json:"webReplicas"`
	ControllerEnabled       bool                 `json:"controllerEnabled"`
	ArtifactImporterEnabled bool                 `json:"artifactImporterEnabled"`
	Gateway                 *GatewayReference    `json:"gateway,omitempty"`
}

type AnvilStatus struct {
	ObservedGeneration  int64              `json:"observedGeneration,omitempty"`
	ConfigurationSHA256 string             `json:"configurationSHA256,omitempty"`
	ReadyReplicas       int32              `json:"readyReplicas,omitempty"`
	Conditions          []metav1.Condition `json:"conditions,omitempty"`
}

// Anvil is a singleton installation in the operator's configured application namespace.
type Anvil struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AnvilSpec   `json:"spec"`
	Status            AnvilStatus `json:"status,omitempty"`
}

type AnvilList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Anvil `json:"items"`
}

func (a *Anvil) DeepCopy() *Anvil                   { return copyJSON(a) }
func (a *Anvil) DeepCopyObject() runtime.Object     { return a.DeepCopy() }
func (a *AnvilList) DeepCopyObject() runtime.Object { return copyJSON(a) }
