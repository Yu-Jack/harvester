package componenthealth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/generated/clientset/versioned/fake"
	"github.com/harvester/harvester/pkg/util"
	"github.com/harvester/harvester/pkg/util/fakeclients"
	longhornv1 "github.com/longhorn/longhorn-manager/k8s/pkg/apis/longhorn/v1beta2"
)

func TestReconcileVolumes(t *testing.T) {
	tests := []struct {
		name           string
		volume         *longhornv1.Volume
		expectedChecks int
		assertCheck    func(*testing.T, v1beta1.CheckResult)
	}{
		{
			name: "false condition reason creates warning check",
			volume: newLonghornVolume("pvc-replica-scheduling-failure", []longhornv1.Condition{
				{
					Type:    longhornv1.VolumeConditionTypeScheduled,
					Status:  longhornv1.ConditionStatusFalse,
					Reason:  longhornv1.VolumeConditionReasonReplicaSchedulingFailure,
					Message: "precheck new replica failed: disks are unavailable",
				},
			}),
			expectedChecks: 1,
			assertCheck: func(t *testing.T, check v1beta1.CheckResult) {
				assert.Equal(t, v1beta1.SeverityWarning, check.Severity)
				assert.Equal(t, "1 Longhorn Volume(s) have ReplicaSchedulingFailure: precheck new replica failed: disks are unavailable", check.Message)
				assert.Equal(t, 1, check.AffectedCount)
				assert.Equal(t, longhornv1.SchemeGroupVersion.String(), check.AffectedResources.APIVersion)
				assert.Equal(t, volumeKind, check.AffectedResources.Kind)
				assert.Contains(t, check.AffectedResources.Names, "pvc-replica-scheduling-failure")
			},
		},
		{
			name: "another false condition reason creates check with reason key",
			volume: newLonghornVolume("pvc-local-replica-scheduling-failure", []longhornv1.Condition{
				{
					Type:   longhornv1.VolumeConditionTypeScheduled,
					Status: longhornv1.ConditionStatusFalse,
					Reason: longhornv1.VolumeConditionReasonLocalReplicaSchedulingFailure,
				},
			}),
			expectedChecks: 1,
			assertCheck: func(t *testing.T, check v1beta1.CheckResult) {
				assert.Equal(t, "1 Longhorn Volume(s) have LocalReplicaSchedulingFailure", check.Message)
				assert.Contains(t, check.AffectedResources.Names, "pvc-local-replica-scheduling-failure")
			},
		},
		{
			name: "non scheduled false condition reason creates check",
			volume: newLonghornVolume("pvc-restore-failure", []longhornv1.Condition{
				{
					Type:   longhornv1.VolumeConditionTypeRestore,
					Status: longhornv1.ConditionStatusFalse,
					Reason: longhornv1.VolumeConditionReasonRestoreFailure,
				},
			}),
			expectedChecks: 1,
			assertCheck: func(t *testing.T, check v1beta1.CheckResult) {
				assert.Equal(t, "1 Longhorn Volume(s) have RestoreFailure", check.Message)
				assert.Contains(t, check.AffectedResources.Names, "pvc-restore-failure")
			},
		},
		{
			name: "true condition reason is ignored",
			volume: newLonghornVolume("healthy-volume", []longhornv1.Condition{
				{
					Type:   longhornv1.VolumeConditionTypeScheduled,
					Status: longhornv1.ConditionStatusTrue,
					Reason: longhornv1.VolumeConditionReasonReplicaSchedulingFailure,
				},
			}),
			expectedChecks: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(tc.volume)
			handler := &Handler{
				componentHealths: fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
				vmiCache:         fakeclients.VirtualMachineInstanceCache(clientset.KubevirtV1().VirtualMachineInstances),
				volumeCache:      fakeclients.LonghornVolumeCache(clientset.LonghornV1beta2().Volumes),
			}

			err := handler.reconcileVolumes()
			assert.NoError(t, err)

			componentHealth, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), volumeComponentHealthName, metav1.GetOptions{})
			assert.NoError(t, err)
			assert.Equal(t, componentName, componentHealth.Labels[v1beta1.LabelKeyComponent])
			assert.Len(t, componentHealth.Status.Checks, tc.expectedChecks)
			if tc.assertCheck != nil {
				check, ok := componentHealth.Status.Checks[tc.volume.Status.Conditions[0].Reason]
				assert.True(t, ok)
				tc.assertCheck(t, check)
			}
		})
	}
}

func TestReconcileVolumesReplacesExistingChecks(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&v1beta1.ComponentHealth{
			ObjectMeta: metav1.ObjectMeta{Name: volumeComponentHealthName},
			Status: v1beta1.ComponentHealthStatus{
				Checks: map[string]v1beta1.CheckResult{
					vmMigrationHealthChecks[0].Rule.Key: {
						Severity: v1beta1.SeverityWarning,
						Message:  "existing VMI check",
					},
					longhornv1.VolumeConditionReasonReplicaSchedulingFailure: {
						Severity: v1beta1.SeverityWarning,
						Message:  "stale volume check",
						AffectedResources: &v1beta1.AffectedResources{
							APIVersion: longhornv1.SchemeGroupVersion.String(),
							Kind:       volumeKind,
						},
					},
				},
			},
		},
		newLonghornVolume("healthy-volume", []longhornv1.Condition{
			{
				Type:   longhornv1.VolumeConditionTypeScheduled,
				Status: longhornv1.ConditionStatusTrue,
			},
		}),
	)
	handler := &Handler{
		componentHealths: fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
		volumeCache:      fakeclients.LonghornVolumeCache(clientset.LonghornV1beta2().Volumes),
	}

	err := handler.reconcileVolumes()
	assert.NoError(t, err)

	componentHealth, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), volumeComponentHealthName, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, componentName, componentHealth.Labels[v1beta1.LabelKeyComponent])
	assert.NotContains(t, componentHealth.Status.Checks, vmMigrationHealthChecks[0].Rule.Key)
	assert.NotContains(t, componentHealth.Status.Checks, longhornv1.VolumeConditionReasonReplicaSchedulingFailure)
}

func newLonghornVolume(name string, conditions []longhornv1.Condition) *longhornv1.Volume {
	return &longhornv1.Volume{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: util.LonghornSystemNamespaceName,
		},
		Status: longhornv1.VolumeStatus{
			Conditions: conditions,
		},
	}
}
