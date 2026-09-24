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
)

func TestDynamicChecks(t *testing.T) {
	nodes := []runtime.Object{
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}, Spec: corev1.NodeSpec{Unschedulable: true}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
	}

	rules, err := parseDynamicFieldRules(`- name: NodeUnschedulable
  componentHealthName: harvester-controller-node
  resource:
    apiVersion: v1
    kind: Node
  fieldPath: spec.unschedulable
  matchValue: "true"
  severity: Warning
  message: Node is marked unschedulable
`)

	assert.NoError(t, err)
	assert.Len(t, rules, 1)
	assert.Equal(t, "NodeUnschedulable", rules[0].Name)
	assert.Equal(t, v1beta1.SeverityWarning, rules[0].Severity)

	matched, err := dynamicFieldMatches(nodes[0], rules[0].FieldPath, rules[0].MatchValue)
	assert.NoError(t, err)
	assert.True(t, matched)
	matched, err = dynamicFieldMatches(nodes[1], rules[0].FieldPath, rules[0].MatchValue)
	assert.NoError(t, err)
	assert.False(t, matched)

	check := buildDynamicCheckResult(rules[0], schema.GroupVersion{Version: "v1"}.WithKind(nodeKind), []string{"node-1"})
	assert.Equal(t, 1, check.AffectedCount)
	assert.Contains(t, check.AffectedResources.Names, "node-1")
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
