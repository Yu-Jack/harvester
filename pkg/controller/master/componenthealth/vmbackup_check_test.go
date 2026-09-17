package componenthealth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/generated/clientset/versioned/fake"
	"github.com/harvester/harvester/pkg/util/fakeclients"
)

func newVMBackup(name string, volumeBackups []v1beta1.VolumeBackup) *v1beta1.VirtualMachineBackup {
	return &v1beta1.VirtualMachineBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Status: v1beta1.VirtualMachineBackupStatus{
			VolumeBackups: volumeBackups,
		},
	}
}

func errorMessage(message string) *string {
	return &message
}

func TestReconcileVMBackups(t *testing.T) {
	older := metav1.NewTime(metav1.Now().Add(-1 * 60 * 1e9)) // 1 minute earlier
	newer := metav1.Now()

	tests := []struct {
		name           string
		vmBackup       *v1beta1.VirtualMachineBackup
		expectedChecks int
		assertCheck    func(*testing.T, v1beta1.CheckResult)
	}{
		{
			name: "latest volume backup with error creates check",
			vmBackup: newVMBackup("failed-vmbackup", []v1beta1.VolumeBackup{
				{
					CreationTime: &older,
					Error:        &v1beta1.Error{Message: errorMessage("old error")},
				},
				{
					CreationTime: &newer,
					Error:        &v1beta1.Error{Message: errorMessage("latest error")},
				},
			}),
			expectedChecks: 1,
			assertCheck: func(t *testing.T, check v1beta1.CheckResult) {
				assert.Equal(t, v1beta1.SeverityError, check.Severity)
				assert.Equal(t, "1 VirtualMachineBackup(s) failed: latest error", check.Message)
				assert.Equal(t, 1, check.AffectedCount)
				assert.Equal(t, v1beta1.SchemeGroupVersion.String(), check.AffectedResources.APIVersion)
				assert.Equal(t, vmBackupKind, check.AffectedResources.Kind)
				assert.Contains(t, check.AffectedResources.Names, "failed-vmbackup")
			},
		},
		{
			name:           "no volume backups creates no check",
			vmBackup:       newVMBackup("healthy-vmbackup", nil),
			expectedChecks: 0,
		},
		{
			name: "volume backup without error creates no check",
			vmBackup: newVMBackup("healthy-vmbackup", []v1beta1.VolumeBackup{
				{CreationTime: &newer},
			}),
			expectedChecks: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(tc.vmBackup)
			handler := &Handler{
				componentHealths: fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
				vmBackupCache:    fakeclients.VMBackupCache(clientset.HarvesterhciV1beta1().VirtualMachineBackups),
			}

			err := handler.reconcileVMBackups()
			assert.NoError(t, err)

			componentHealth, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), vmBackupComponentHealthName, metav1.GetOptions{})
			assert.NoError(t, err)
			assert.Len(t, componentHealth.Status.Checks, tc.expectedChecks)
			if tc.assertCheck != nil {
				check, ok := componentHealth.Status.Checks[checkKeyVMBackupFailed]
				assert.True(t, ok)
				tc.assertCheck(t, check)
			}
		})
	}
}
