package v1beta1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type Severity string

const (
	SeverityError   Severity = "Error"
	SeverityWarning Severity = "Warning"
	SeverityInfo    Severity = "Info"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:scope=Cluster

// ComponentHealth reports the latest health check results of a single Harvester component.
type ComponentHealth struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status ComponentHealthStatus `json:"status,omitempty"`
}

type ComponentHealthStatus struct {
	// +optional
	Node string `json:"node,omitempty"`
	// +optional
	LastCheckedAt metav1.Time `json:"lastCheckedAt,omitempty"`
	// +optional
	Checks map[string]CheckResult `json:"checks,omitempty"`
}

type CheckResult struct {
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	// +optional
	AffectedCount int `json:"affectedCount,omitempty"`
	// +optional
	Truncated bool `json:"truncated,omitempty"`
	// +optional
	AffectedResources *AffectedResources `json:"affectedResources,omitempty"`
}

type AffectedResources struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	// +optional
	Names map[string]AffectedResourceDetail `json:"names,omitempty"`
}

type AffectedResourceDetail struct{}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:scope=Cluster

// HealthSummary aggregates all ComponentHealth resources into a single cluster-wide overview.
// There is exactly one HealthSummary resource, named "cluster".
type HealthSummary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status HealthSummaryStatus `json:"status,omitempty"`
}

type HealthSummaryStatus struct {
	// +optional
	LastCheckedAt metav1.Time `json:"lastCheckedAt,omitempty"`
	// +optional
	Components map[string]ComponentSummary `json:"components,omitempty"`
}

type ComponentSummary struct {
	ErrorCount   int `json:"errorCount"`
	WarningCount int `json:"warningCount"`
}
