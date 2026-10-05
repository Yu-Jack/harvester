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

func TestDeletionNotificationsClearComponentHealthChecks(t *testing.T) {
	tests := []struct {
		name                string
		componentHealthName string
		onDelete            func(*testing.T, *Handler) error
	}{
		{
			name:                "node",
			componentHealthName: nodeComponentHealthName,
			onDelete: func(t *testing.T, handler *Handler) error {
				result, err := handler.OnNodeChanged("deleted-node", nil)
				assert.Nil(t, result)
				return err
			},
		},
		{
			name:                "vmi",
			componentHealthName: vmComponentHealthName,
			onDelete: func(t *testing.T, handler *Handler) error {
				result, err := handler.OnVMIChanged("default/deleted-vmi", nil)
				assert.Nil(t, result)
				return err
			},
		},
		{
			name:                "volume",
			componentHealthName: volumeComponentHealthName,
			onDelete: func(t *testing.T, handler *Handler) error {
				result, err := handler.OnVolumeChanged("longhorn-system/deleted-volume", nil)
				assert.Nil(t, result)
				return err
			},
		},
		{
			name:                "scheduled backup",
			componentHealthName: scheduleVMBackupComponentHealthName,
			onDelete: func(t *testing.T, handler *Handler) error {
				result, err := handler.OnScheduleVMBackupChanged("default/deleted-schedule", nil)
				assert.Nil(t, result)
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(&v1beta1.ComponentHealth{
				ObjectMeta: metav1.ObjectMeta{Name: tc.componentHealthName},
				Status: v1beta1.ComponentHealthStatus{
					Checks: map[string]v1beta1.CheckResult{
						"StaleCheck": {Severity: v1beta1.SeverityError, Message: "deleted resource failed"},
					},
				},
			})
			controller := &recordingComponentHealthController{}
			handler := &Handler{
				componentHealthController: controller,
				componentHealths:          fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
				nodeCache:                 fakeclients.NodeCache(clientset.CoreV1().Nodes),
				vmiCache:                  fakeclients.VirtualMachineInstanceCache(clientset.KubevirtV1().VirtualMachineInstances),
				volumeCache:               fakeclients.LonghornVolumeCache(clientset.LonghornV1beta2().Volumes),
				scheduleVMBackupCache:     fakeclients.SVMBackupCache(clientset.HarvesterhciV1beta1().ScheduleVMBackups),
			}
			if !assert.NoError(t, tc.onDelete(t, handler)) {
				return
			}
			if !assert.Equal(t, []string{tc.componentHealthName}, controller.enqueued) {
				return
			}
			_, err := handler.OnComponentHealthChanged(controller.enqueued[0], nil)
			if !assert.NoError(t, err) {
				return
			}
			health, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), tc.componentHealthName, metav1.GetOptions{})
			if assert.NoError(t, err) {
				assert.Empty(t, health.Status.Checks)
			}
		})
	}
}

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
			expectedChecks: 2,
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

func TestReconcileNodesReplacesExistingChecks(t *testing.T) {
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
	assert.NotContains(t, componentHealth.Status.Checks, "OtherNodeCheck")
}

func TestReconcileNodesDoesNotUpdateEmptyChecks(t *testing.T) {
	clientset := fake.NewSimpleClientset(&v1beta1.ComponentHealth{
		ObjectMeta: metav1.ObjectMeta{Name: nodeComponentHealthName},
	})
	handler := &Handler{
		componentHealths: fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
		nodeCache:        fakeclients.NodeCache(clientset.CoreV1().Nodes),
	}

	err := handler.reconcileNodes()
	assert.NoError(t, err)

	componentHealth, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), nodeComponentHealthName, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Nil(t, componentHealth.Status.Checks)
}

func newComponentHealthNode(name string, unschedulable bool) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.NodeSpec{
			Unschedulable: unschedulable,
		},
	}
}
