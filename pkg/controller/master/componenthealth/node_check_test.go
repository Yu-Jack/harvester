package componenthealth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/generated/clientset/versioned/fake"
	"github.com/harvester/harvester/pkg/util/fakeclients"
)

func TestReconcileNodes(t *testing.T) {
	tests := []struct {
		name           string
		nodes          []corev1.Node
		expectedChecks int
		assertCheck    func(*testing.T, v1beta1.CheckResult)
	}{
		{
			name: "cordoned node creates info check",
			nodes: []corev1.Node{
				newComponentHealthNode("node-1", true),
				newComponentHealthNode("node-2", false),
			},
			expectedChecks: 1,
			assertCheck: func(t *testing.T, check v1beta1.CheckResult) {
				assert.Equal(t, v1beta1.SeverityInfo, check.Severity)
				assert.Equal(t, "1 Node(s) are cordoned", check.Message)
				assert.Equal(t, 1, check.AffectedCount)
				assert.Equal(t, corev1.SchemeGroupVersion.String(), check.AffectedResources.APIVersion)
				assert.Equal(t, nodeKind, check.AffectedResources.Kind)
				assert.Contains(t, check.AffectedResources.Names, "node-1")
				assert.NotContains(t, check.AffectedResources.Names, "node-2")
			},
		},
		{
			name: "schedulable nodes have no check",
			nodes: []corev1.Node{
				newComponentHealthNode("node-1", false),
			},
			expectedChecks: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objects := make([]runtime.Object, 0, len(tc.nodes))
			for index := range tc.nodes {
				objects = append(objects, &tc.nodes[index])
			}

			clientset := fake.NewSimpleClientset(objects...)
			handler := &Handler{
				componentHealths: fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
				nodeCache:        fakeclients.NodeCache(clientset.CoreV1().Nodes),
			}

			err := handler.reconcileNodes()
			assert.NoError(t, err)

			componentHealth, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), nodeComponentHealthName, metav1.GetOptions{})
			assert.NoError(t, err)
			assert.Equal(t, componentName, componentHealth.Labels[v1beta1.LabelKeyComponent])
			assert.Len(t, componentHealth.Status.Checks, tc.expectedChecks)
			if tc.assertCheck != nil {
				check, ok := componentHealth.Status.Checks[checkKeyNodeCordoned]
				assert.True(t, ok)
				tc.assertCheck(t, check)
			}
		})
	}
}

func TestReconcileNodesPreservesUnownedChecks(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&v1beta1.ComponentHealth{
			ObjectMeta: metav1.ObjectMeta{Name: nodeComponentHealthName},
			Status: v1beta1.ComponentHealthStatus{
				Checks: map[string]v1beta1.CheckResult{
					checkKeyNodeCordoned: {
						Severity: v1beta1.SeverityInfo,
						Message:  "stale cordoned node check",
					},
					"OtherNodeCheck": {
						Severity: v1beta1.SeverityWarning,
						Message:  "existing node check",
					},
				},
			},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		},
	)
	handler := &Handler{
		componentHealths: fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
		nodeCache:        fakeclients.NodeCache(clientset.CoreV1().Nodes),
	}

	err := handler.reconcileNodes()
	assert.NoError(t, err)

	componentHealth, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), nodeComponentHealthName, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.NotContains(t, componentHealth.Status.Checks, checkKeyNodeCordoned)
	assert.Contains(t, componentHealth.Status.Checks, "OtherNodeCheck")
}

func newComponentHealthNode(name string, unschedulable bool) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.NodeSpec{
			Unschedulable: unschedulable,
		},
	}
}
