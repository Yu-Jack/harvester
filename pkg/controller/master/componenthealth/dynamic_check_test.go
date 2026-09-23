package componenthealth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/generated/clientset/versioned/fake"
	"github.com/harvester/harvester/pkg/util/fakeclients"
)

func TestDynamicChecks(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dynamicFieldsConfigMapName,
			Namespace: dynamicFieldsNamespace,
		},
		Data: map[string]string{
			dynamicFieldsDataKey: "- name: NodeUnschedulable\n  componentHealthName: harvester-controller-node\n  resource:\n    apiVersion: v1\n    kind: Node\n  fieldPath: spec.unschedulable\n  matchValue: \"true\"\n  severity: Warning\n  message: Node is marked unschedulable\n",
		},
	})
	handler := &Handler{
		configMapCache: fakeclients.ConfigmapCache(clientset.CoreV1().ConfigMaps),
	}
	nodes := []runtime.Object{
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}, Spec: corev1.NodeSpec{Unschedulable: true}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
	}

	checks, ownedKeys, err := handler.dynamicChecks(nodeComponentHealthName, schema.GroupVersion{Version: "v1"}.WithKind(nodeKind), nodes)

	assert.NoError(t, err)
	assert.Equal(t, []string{"NodeUnschedulable"}, ownedKeys)
	assert.Equal(t, v1beta1.SeverityWarning, checks["NodeUnschedulable"].Severity)
	assert.Equal(t, "Node is marked unschedulable", checks["NodeUnschedulable"].Message)
	assert.Equal(t, 1, checks["NodeUnschedulable"].AffectedCount)
	assert.Contains(t, checks["NodeUnschedulable"].AffectedResources.Names, "node-1")
	assert.NotContains(t, checks["NodeUnschedulable"].AffectedResources.Names, "node-2")
}

func TestDynamicFieldRuleValidation(t *testing.T) {
	tests := []struct {
		name string
		rule dynamicFieldRule
	}{
		{
			name: "nested scalar field",
			rule: dynamicFieldRule{
				Name:                "NestedField",
				ComponentHealthName: nodeComponentHealthName,
				Resource:            dynamicFieldResource{APIVersion: "v1", Kind: "Node"},
				FieldPath:           "status.conditions.reason",
				Severity:            v1beta1.SeverityInfo,
				Message:             "nested field matched",
			},
		},
		{
			name: "unsupported resource",
			rule: dynamicFieldRule{Resource: dynamicFieldResource{APIVersion: "v1", Kind: "Pod"}, FieldPath: "spec.phase"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "nested scalar field" {
				assert.NoError(t, validateDynamicFieldRule(tc.rule))
				return
			}
			assert.Error(t, validateDynamicFieldRule(tc.rule))
		})
	}
}

func TestDynamicFieldMatchesNestedScalar(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]interface{}{
		"status": map[string]interface{}{
			"guestOSInfo": map[string]interface{}{
				"id": "centos",
			},
		},
	}}

	matched, err := dynamicFieldMatches(object, "status.guestOSInfo.id", "centos")

	assert.NoError(t, err)
	assert.True(t, matched)
}

func TestDynamicFieldMatchesRejectsArray(t *testing.T) {
	node := &corev1.Node{Spec: corev1.NodeSpec{Taints: []corev1.Taint{{Key: "maintenance"}}}}
	matched, err := dynamicFieldMatches(node, "spec.taints", "")

	assert.False(t, matched)
	assert.Error(t, err)
}
