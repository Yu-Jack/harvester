package componenthealth

import (
	"context"
	"testing"

	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/harvester/harvester/pkg/apis/harvesterhci.io/v1beta1"
	"github.com/harvester/harvester/pkg/generated/clientset/versioned/fake"
	"github.com/harvester/harvester/pkg/util/fakeclients"
)

func newScheduleVMBackup(name string, conditions []v1beta1.Condition) *v1beta1.ScheduleVMBackup {
	return &v1beta1.ScheduleVMBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Status: v1beta1.ScheduleVMBackupStatus{
			Conditions: conditions,
		},
	}
}

func TestReconcileScheduleVMBackups(t *testing.T) {
	tests := []struct {
		name             string
		scheduleVMBackup *v1beta1.ScheduleVMBackup
		expectedChecks   int
		assertCheck      func(*testing.T, v1beta1.CheckResult)
	}{
		{
			name: "true condition reason creates warning check",
			scheduleVMBackup: newScheduleVMBackup("suspended-svmbackup", []v1beta1.Condition{
				{
					Type:    condition.Cond("BackupSuspend"),
					Status:  corev1.ConditionTrue,
					Reason:  "Reach Max Failure",
					Message: "failure backups 2 reach max tolerance 2",
				},
			}),
			expectedChecks: 1,
			assertCheck: func(t *testing.T, check v1beta1.CheckResult) {
				assert.Equal(t, v1beta1.SeverityError, check.Severity)
				assert.Equal(t, "1 ScheduleVMBackup(s) have Reach Max Failure: failure backups 2 reach max tolerance 2", check.Message)
				assert.Equal(t, 1, check.AffectedCount)
				assert.Equal(t, v1beta1.SchemeGroupVersion.String(), check.AffectedResources.APIVersion)
				assert.Equal(t, scheduleVMBackupKind, check.AffectedResources.Kind)
				assert.Contains(t, check.AffectedResources.Names, "suspended-svmbackup")
			},
		},
		{
			name: "false condition reason is ignored",
			scheduleVMBackup: newScheduleVMBackup("healthy-svmbackup", []v1beta1.Condition{
				{
					Type:   condition.Cond("BackupSuspend"),
					Status: corev1.ConditionFalse,
					Reason: "Reach Max Failure",
				},
			}),
			expectedChecks: 0,
		},
		{
			name:             "no conditions creates no check",
			scheduleVMBackup: newScheduleVMBackup("no-condition-svmbackup", nil),
			expectedChecks:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(tc.scheduleVMBackup)
			handler := &Handler{
				componentHealths:      fakeclients.ComponentHealthClient(clientset.HarvesterhciV1beta1().ComponentHealths),
				scheduleVMBackupCache: fakeclients.SVMBackupCache(clientset.HarvesterhciV1beta1().ScheduleVMBackups),
			}

			err := handler.reconcileScheduleVMBackups()
			assert.NoError(t, err)

			componentHealth, err := clientset.HarvesterhciV1beta1().ComponentHealths().Get(context.Background(), scheduleVMBackupComponentHealthName, metav1.GetOptions{})
			assert.NoError(t, err)
			assert.Len(t, componentHealth.Status.Checks, tc.expectedChecks)
			if tc.assertCheck != nil {
				check, ok := componentHealth.Status.Checks[tc.scheduleVMBackup.Status.Conditions[0].Reason]
				assert.True(t, ok)
				tc.assertCheck(t, check)
			}
		})
	}
}
